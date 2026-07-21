package swarmsearch

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/swartznet/swartznet/contracts/ltepwire"
)

// announceQueueDepth bounds the outbound peer_announce worker; overflow drops.
const announceQueueDepth = 64

// Capabilities is the node's own advertised capability set: the operator
// sharing prefs (for inbound scope/reject decisions) and the live 64-bit
// services mask (for the outbound peer_announce). The engine supplies both from
// the single ltepwire.Announced producer.
type Capabilities struct {
	Sharing  ltepwire.Sharing
	Services uint64
}

// PeerState is what we know about one remote peer.
type PeerState struct {
	Addr            string
	SeenAt          time.Time
	Supported       bool // advertised sn_search in its LTEP m dict
	RemoteExtID     int
	token           PeerToken
	Services        ltepwire.ServiceBits
	Version         int
	PublisherPubkey [32]byte
	hasPubkey       bool
	gossipedTo      bool // we've sent this peer an sn_peers introduction (once)
}

type announceReq struct {
	token  PeerToken
	gossip bool // true → send an sn_peers PEX frame instead of a peer_announce
}

// Protocol is the sn_search peer-wire engine. It is owned by the engine, which
// injects the transport, the local searcher, and the capability source, and
// drives it through the anacrolix LTEP callbacks. Safe for concurrent use.
type Protocol struct {
	log *slog.Logger

	mu        sync.Mutex
	peers     map[string]*PeerState
	searcher  LocalSearcher
	transport Transport
	capsFn    func() Capabilities
	pubkey    [32]byte
	hasPub    bool
	epoch     uint64 // bumped per (addr) mint so a stale token cannot resolve

	idxSink  IndexerSink
	endSink  EndorsementSink
	peerSink PeerSink

	// Sync (Slice 8): record substrate + reconciliation session registry.
	recordSource RecordSource
	recordSink   RecordSink
	pubObserver  PublisherObserver
	syncMu       sync.Mutex
	syncSessions map[string]map[uint32]*SyncSession // (peerAddr, txid)

	limiter *rateLimiter
	ban     *banman

	// pending outbound queries, keyed by txid, guarded by a SEPARATE mutex so
	// a query awaiting results never blocks peer-state updates.
	pendingMu sync.Mutex
	pending   map[uint32]*pendingQuery
	txid      uint32

	announceCh chan announceReq
	closeOnce  sync.Once
	done       chan struct{}
}

// New constructs a Protocol and starts its outbound-announce worker.
func New(log *slog.Logger) *Protocol {
	if log == nil {
		log = slog.Default()
	}
	p := &Protocol{
		log:          log,
		peers:        make(map[string]*PeerState),
		pending:      make(map[uint32]*pendingQuery),
		limiter:      newRateLimiter(DefaultRateLimit()),
		ban:          newBanman(),
		announceCh:   make(chan announceReq, announceQueueDepth),
		done:         make(chan struct{}),
		syncSessions: make(map[string]map[uint32]*SyncSession),
	}
	go p.announceWorker()
	return p
}

// Close stops the background workers.
func (p *Protocol) Close() {
	p.closeOnce.Do(func() { close(p.done) })
}

// ---- injection (all nil-tolerant, mutex-guarded) ----

// SetSearcher installs (or clears) the local searcher backing inbound queries.
func (p *Protocol) SetSearcher(s LocalSearcher) {
	p.mu.Lock()
	p.searcher = s
	p.mu.Unlock()
}

// SetTransport installs the engine sender used for outbound frames.
func (p *Protocol) SetTransport(t Transport) {
	p.mu.Lock()
	p.transport = t
	p.mu.Unlock()
}

// SetCapabilitySource installs the func the protocol calls to learn our own
// advertised capabilities (sharing prefs + live services mask).
func (p *Protocol) SetCapabilitySource(fn func() Capabilities) {
	p.mu.Lock()
	p.capsFn = fn
	p.mu.Unlock()
}

// SetPublisherPubkey sets the 32-byte publisher key gossiped in peer_announce
// (only sent when the node is actively publishing). A zero key disables it.
func (p *Protocol) SetPublisherPubkey(pk [32]byte, has bool) {
	p.mu.Lock()
	p.pubkey, p.hasPub = pk, has
	p.mu.Unlock()
}

// SetIndexerSink / SetEndorsementSink install the gossip sinks (no-op stubs in
// Slice 7).
func (p *Protocol) SetIndexerSink(s IndexerSink)         { p.mu.Lock(); p.idxSink = s; p.mu.Unlock() }
func (p *Protocol) SetEndorsementSink(s EndorsementSink) { p.mu.Lock(); p.endSink = s; p.mu.Unlock() }

// SetPeerSink installs the sink for sn_peers-learned addresses (nil-tolerant).
func (p *Protocol) SetPeerSink(s PeerSink) { p.mu.Lock(); p.peerSink = s; p.mu.Unlock() }

// SetRateLimit swaps the inbound-query rate limit.
func (p *Protocol) SetRateLimit(rl RateLimit) { p.limiter.setConfig(rl) }

// ---- LTEP negotiation callbacks (driven by the engine) ----

// NotePeerAdded records a new connection (before the extended handshake). The
// peer is not yet addressable for sn_search until it advertises.
func (p *Protocol) NotePeerAdded(addr string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.peers[addr]; !ok {
		p.peers[addr] = &PeerState{Addr: addr, SeenAt: time.Now()}
	}
}

// OnRemoteHandshake records the remote's advertised sn_search support from its
// LTEP m dict. `supported` is true iff m["sn_search"] is present with a
// non-zero id. A banned peer is never marked Supported. When a peer newly
// advertises, its PeerToken is minted (the ONLY mint site) and an outbound
// peer_announce is enqueued.
func (p *Protocol) OnRemoteHandshake(addr string, supported bool, remoteExtID int) {
	if p.ban.IsBanned(addr) {
		return
	}
	p.mu.Lock()
	ps := p.peers[addr]
	if ps == nil {
		ps = &PeerState{Addr: addr, SeenAt: time.Now()}
		p.peers[addr] = ps
	}
	wasSupported := ps.Supported
	ps.RemoteExtID = remoteExtID
	var tok PeerToken
	if supported && remoteExtID != 0 {
		ps.Supported = true
		if !wasSupported {
			p.epoch++
			ps.token = PeerToken{addr: addr, epoch: p.epoch}
		}
		tok = ps.token
	}
	newlySupported := supported && remoteExtID != 0 && !wasSupported
	p.mu.Unlock()

	if newlySupported {
		// Enqueue the announce off the read loop (non-blocking, drop on full).
		select {
		case p.announceCh <- announceReq{token: tok}:
		default:
			p.log.Debug("swarmsearch.announce_dropped_queue_full", "addr", addr)
		}
	}
}

// OnPeerClosed drops per-peer state (keeps bans) on disconnect, and releases any
// in-flight sync sessions so their symbol pumps stop immediately instead of
// running to budget exhaustion against a dead connection + lingering until the
// lazy reaper.
func (p *Protocol) OnPeerClosed(addr string) {
	p.mu.Lock()
	delete(p.peers, addr)
	p.mu.Unlock()
	p.limiter.forget(addr)
	p.ban.Forget(addr)
	p.releaseAllSyncSessions(addr)
}

// KnownPeers returns the number of peers we have any state for.
func (p *Protocol) KnownPeers() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.peers)
}

// CapablePeerCount returns the number of peers that advertised sn_search.
func (p *Protocol) CapablePeerCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, ps := range p.peers {
		if ps.Supported {
			n++
		}
	}
	return n
}

func (p *Protocol) nextTxID() uint32 { return atomic.AddUint32(&p.txid, 1) }

// caps reads our own advertised capabilities (nil source ⇒ zero).
func (p *Protocol) caps() Capabilities {
	p.mu.Lock()
	fn := p.capsFn
	p.mu.Unlock()
	if fn == nil {
		return Capabilities{}
	}
	return fn()
}

// announceWorker sends outbound peer_announce frames off the read loop.
func (p *Protocol) announceWorker() {
	for {
		select {
		case <-p.done:
			return
		case req := <-p.announceCh:
			if req.gossip {
				p.sendGossip(req.token)
			} else {
				p.sendAnnounce(req.token)
			}
		}
	}
}

func (p *Protocol) sendAnnounce(tok PeerToken) {
	p.mu.Lock()
	t := p.transport
	pk, hasPub := p.pubkey, p.hasPub
	p.mu.Unlock()
	if t == nil || !tok.valid() {
		return
	}
	pa := ltepwire.PeerAnnounce{Services: p.caps().Services}
	if hasPub {
		pa.Pk = pk[:]
	}
	frame, err := ltepwire.EncodePeerAnnounce(pa)
	if err != nil {
		p.log.Debug("swarmsearch.announce_encode_err", "err", err)
		return
	}
	if err := t.SendExtension(tok, frame); err != nil {
		p.log.Debug("swarmsearch.announce_send_err", "addr", tok.Addr(), "err", err)
	}
}
