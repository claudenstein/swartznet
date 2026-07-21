package engine

import (
	"fmt"
	"net"
	"sort"
	"strconv"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

// swarmPeerSink adapts the engine to swarmsearch.PeerSink: sn_peers-learned
// addresses are introduced to every joined rendezvous swarm.
type swarmPeerSink struct{ eng *Engine }

func (s swarmPeerSink) AddDiscoveredPeers(addrs []string) { s.eng.AddRendezvousPeers(addrs) }

// AddRendezvous joins a rendezvous swarm by infohash: a metadata-less torrent
// used purely as a mainline-DHT meeting point where sn_search-capable peers find
// and handshake each other (see internal/rendezvous for the derivation). It
// never downloads content or acts on metadata, is never indexed, and is hidden
// from the user-facing torrent list. Idempotent per infohash.
func (e *Engine) AddRendezvous(hash metainfo.Hash) (*Handle, error) {
	if hash.IsZero() {
		return nil, fmt.Errorf("engine: zero rendezvous infohash")
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, fmt.Errorf("engine: closed")
	}
	t, _ := e.client.AddTorrentInfoHash(hash)
	h, existed := e.registerLockedRendezvous(t)
	e.mu.Unlock()
	if !existed {
		// A handshake meeting point only: never request pieces. This also
		// neutralizes metadata-poisoning of the well-known infohash — even if a
		// peer injects a metainfo, no content is ever downloaded.
		t.DisallowDataDownload()
	}
	return h, nil
}

// RemoveRendezvous leaves a rendezvous swarm. It is a no-op (nil error) if the
// infohash is not joined, and errors if the infohash names a normal torrent
// rather than a rendezvous swarm (so a caller can't accidentally drop a download
// through this path).
func (e *Engine) RemoveRendezvous(hash metainfo.Hash) error {
	e.mu.Lock()
	h, ok := e.handles[hash]
	if !ok {
		e.mu.Unlock()
		return nil
	}
	if !h.rendezvous {
		e.mu.Unlock()
		return fmt.Errorf("engine: %s is not a rendezvous swarm", hash.HexString())
	}
	delete(e.handles, hash)
	e.mu.Unlock()

	h.markRemoved()
	h.pieceSub.Close()
	h.fileSub.Close()
	h.T.Drop()
	return nil
}

// RendezvousInfoHashes returns the hex infohashes of the rendezvous swarms
// currently joined, sorted. Used by the manager's reconcile and status views.
func (e *Engine) RendezvousInfoHashes() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, 0)
	for ih, h := range e.handles {
		if h.rendezvous {
			out = append(out, ih.HexString())
		}
	}
	sort.Strings(out)
	return out
}

// AddRendezvousPeers introduces the given addresses to EVERY joined rendezvous
// swarm — the shared-infohash context an sn_search handshake requires. This is
// the sink for sn_peers PEX: an address learned from gossip has nowhere to
// connect on its own (BitTorrent connections are per-torrent), so we hand it to
// the rendezvous torrents and anacrolix dials it there. Only numeric ip:port
// addresses are accepted — hostnames are rejected outright so a hostile peer
// cannot make us perform a DNS lookup. Returns the number of (swarm,peer)
// introductions attempted.
func (e *Engine) AddRendezvousPeers(addrs []string) int {
	e.mu.Lock()
	var rvs []*Handle
	for _, h := range e.handles {
		if h.rendezvous {
			rvs = append(rvs, h)
		}
	}
	e.mu.Unlock()
	if len(rvs) == 0 {
		return 0
	}

	peers := parseNumericPeers(addrs)
	if len(peers) == 0 {
		return 0
	}
	n := 0
	for _, h := range rvs {
		n += h.T.AddPeers(peers)
	}
	return n
}

// parseNumericPeers turns "ip:port" strings into anacrolix PeerInfos, rejecting
// anything that is not a numeric IPv4/IPv6 address with a valid port. Hostnames
// are refused outright so untrusted (PEX-supplied) input can never trigger a DNS
// lookup.
func parseNumericPeers(addrs []string) []torrent.PeerInfo {
	peers := make([]torrent.PeerInfo, 0, len(addrs))
	for _, a := range addrs {
		host, portStr, err := net.SplitHostPort(a)
		if err != nil {
			continue
		}
		ip := net.ParseIP(host)
		if ip == nil { // reject hostnames: no DNS on attacker-supplied input
			continue
		}
		port, err := strconv.Atoi(portStr)
		if err != nil || port <= 0 || port > 65535 {
			continue
		}
		peers = append(peers, torrent.PeerInfo{
			Addr:   &net.TCPAddr{IP: ip, Port: port},
			Source: torrent.PeerSourcePex,
		})
	}
	return peers
}
