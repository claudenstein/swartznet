package swarmsearch

import (
	"log/slog"
	"sync"
	"time"

	pp "github.com/anacrolix/torrent/peer_protocol"
)

// ExtensionName is the BEP-10 extension name we advertise. Chosen with
// a `sn_` prefix per docs/05-integration-design.md §5.1 to stay out of
// libtorrent's historical `lt_` namespace.
const ExtensionName pp.ExtensionName = "sn_search"

// ProtocolVersion is the sn_search on-the-wire version. Bumped when we
// make a backwards-incompatible change; advertised in the LTEP
// handshake dictionary alongside the extension name (M3b).
const ProtocolVersion = 1

// Capabilities describes what this node is willing to do when talking
// sn_search. Mirrors the `sn_search_cap` string from
// docs/05-integration-design.md §5.1 but stored as typed fields for
// the runtime; we only serialise to the string form when writing the
// LTEP handshake.
type Capabilities struct {
	// ShareLocal == 0 → don't answer queries at all.
	// ShareLocal == 1 → answer queries for torrents in the current swarm.
	// ShareLocal == 2 → answer queries for any torrent in the local index.
	ShareLocal int
	// FileHits is 1 when this node can return per-file hits
	// (Layer L F2+). 0 when only torrent-name hits.
	FileHits int
	// ContentHits is 1 when this node indexes file contents
	// (Layer L F3). 0 otherwise.
	ContentHits int
	// Publisher is 1 when this node publishes keyword entries to the
	// mainline DHT (Layer D, landing in M4). 0 otherwise.
	Publisher int
}

// DefaultCapabilities returns the capability set every SwartzNet node
// enables by default: share the entire local index, serve file-list
// and content hits, do not publish to the DHT yet (M4).
func DefaultCapabilities() Capabilities {
	return Capabilities{
		ShareLocal:  2,
		FileHits:    1,
		ContentHits: 1,
		Publisher:   0,
	}
}

// PeerState is what the Protocol remembers about each peer it has
// seen. It is not the full anacrolix *PeerConn — we intentionally keep
// this minimal so the peer-tracking map doesn't become a memory hog on
// large swarms.
type PeerState struct {
	// Addr is the peer's remote address in "ip:port" form.
	Addr string
	// Supported is true iff the peer advertised sn_search in its
	// LTEP handshake (i.e. the remote `m` dict contained our
	// extension name with a non-zero id).
	Supported bool
	// RemoteExtID is the extension id the peer assigned to sn_search
	// in their own `m` dict. We address outbound messages to them
	// with this id inside an ID-20 frame.
	RemoteExtID pp.ExtensionNumber
	// Version is the peer's advertised protocol version, if any.
	Version int
	// SeenAt is when we most recently exchanged an LTEP handshake
	// with this peer. Used for evicting stale entries.
	SeenAt time.Time
}

// Protocol is the central state for the sn_search extension. A single
// Protocol is owned by an *engine.Engine; external code (the CLI, the
// future REST layer) reaches it via Engine.SwarmSearch().
type Protocol struct {
	log  *slog.Logger
	caps Capabilities

	mu       sync.RWMutex
	peers    map[string]*PeerState // keyed by peer addr ("ip:port")
	searcher LocalSearcher         // attached in M3b; nil means "reject all inbound queries"
	sender   Sender                // attached by engine; nil means "decode only, don't reply"

	// limiter is the M12f per-peer inbound query rate limiter.
	// Constructed in New() with DefaultRateLimit(). Nil-safe —
	// handler.go tolerates a nil limiter for the test harness.
	limiter *rateLimiter

	// misbehavior is the M15c per-peer misbehavior-score tracker
	// (defence in depth on top of the rate limiter). Always
	// non-nil after New. Peers that accumulate score >=
	// BanThreshold are added to a local banlist and rejected on
	// future handshakes. Local-only — never gossiped.
	misbehavior *misbehaviorTracker

	// txidCounter is incremented by nextTxID() for each outbound
	// Query fan-out (M3c). Accessed with sync/atomic.
	txidCounter uint32

	// pendingMu guards pending. Separate from mu because we hold
	// pendingMu across channel sends in routeResult, and we don't
	// want that to block peer-state updates.
	pendingMu sync.RWMutex
	pending   map[uint32]*pendingQuery
}

// New constructs a Protocol with default capabilities, the
// production rate limiter (DefaultRateLimit), and the
// misbehavior score tracker.
func New(log *slog.Logger) *Protocol {
	if log == nil {
		log = slog.Default()
	}
	return &Protocol{
		log:         log,
		caps:        DefaultCapabilities(),
		peers:       make(map[string]*PeerState),
		limiter:     newRateLimiter(DefaultRateLimit()),
		misbehavior: newMisbehaviorTracker(),
	}
}

// MisbehaviorScore returns the current misbehavior score for
// the peer at addr, or 0 if no record exists. Primarily used
// by tests and /status output.
func (p *Protocol) MisbehaviorScore(addr string) int {
	if p.misbehavior == nil {
		return 0
	}
	return p.misbehavior.Score(addr)
}

// IsBanned reports whether the peer at addr is in the local
// banlist. Expired bans auto-clear.
func (p *Protocol) IsBanned(addr string) bool {
	if p.misbehavior == nil {
		return false
	}
	return p.misbehavior.IsBanned(addr)
}

// SetRateLimit replaces the per-peer inbound query rate-limit
// configuration at runtime. Pass a zero RateLimit to disable
// limiting entirely. Existing per-peer buckets are left intact
// and will fill at the new rate on their next query.
func (p *Protocol) SetRateLimit(cfg RateLimit) {
	if p.limiter == nil {
		p.limiter = newRateLimiter(cfg)
		return
	}
	p.limiter.setConfig(cfg)
}

// Capabilities returns a copy of the current capability set.
func (p *Protocol) Capabilities() Capabilities {
	return p.caps
}

// SetCapabilities replaces the capability set. Safe for concurrent
// use; changes take effect on the next peer handshake.
func (p *Protocol) SetCapabilities(c Capabilities) {
	p.mu.Lock()
	p.caps = c
	p.mu.Unlock()
}

// KnownPeers returns a snapshot of every peer the protocol has a
// record for. The returned slice is freshly allocated so the caller
// can iterate without holding the protocol lock.
func (p *Protocol) KnownPeers() []PeerState {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]PeerState, 0, len(p.peers))
	for _, ps := range p.peers {
		out = append(out, *ps)
	}
	return out
}

// CapablePeerCount returns the number of known peers that advertised
// sn_search in their handshake.
func (p *Protocol) CapablePeerCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var n int
	for _, ps := range p.peers {
		if ps.Supported {
			n++
		}
	}
	return n
}

// LtepAdvertiser is the narrow interface we need from a peer's local
// extension-protocol map in order to register sn_search with it. The
// real anacrolix/torrent type is *torrent.LocalLtepProtocolMap; we
// take it via an interface so protocol.go has no hard dependency on
// the torrent package, which makes the state logic unit-testable.
type LtepAdvertiser interface {
	AddUserProtocol(name pp.ExtensionName)
}

// NotePeerAdded records that we have a new connection to the given
// peer address. The caller (usually engine.go) should also call
// AdvertiseOn with the peer's LocalLtepProtocolMap so our next LTEP
// handshake advertises sn_search.
//
// Split into two calls because AdvertiseOn needs the anacrolix-specific
// map type while NotePeerAdded is pure state — unit tests exercise
// NotePeerAdded directly.
func (p *Protocol) NotePeerAdded(addr string) {
	p.mu.Lock()
	if _, ok := p.peers[addr]; !ok {
		p.peers[addr] = &PeerState{Addr: addr, SeenAt: time.Now()}
	}
	p.mu.Unlock()
	p.log.Debug("swarmsearch.peer_added", "peer", addr)
}

// AdvertiseOn registers "sn_search" in the given peer's local
// extension-protocol map so the next outbound LTEP handshake will
// include it. Must be called from inside the PeerConnAdded callback,
// per anacrolix/torrent's callback contract.
func (p *Protocol) AdvertiseOn(m LtepAdvertiser) {
	m.AddUserProtocol(ExtensionName)
}

// OnRemoteHandshake is called after the remote peer sends its LTEP
// handshake. It inspects the remote peer's `m` dictionary to see
// whether they advertise sn_search; if so, we record their chosen
// extension id and mark the peer as supported. A peer that does NOT
// advertise sn_search is still tracked (we know they exist) but
// marked as unsupported so outbound queries skip them.
//
// M15c: peers in the local banlist are rejected here — they're
// never marked as sn_search-capable regardless of what they
// advertise, so outbound queries skip them and inbound handler
// paths (handleQuery et al) early-return via the ban check.
func (p *Protocol) OnRemoteHandshake(addr string, hs *pp.ExtendedHandshakeMessage) {
	if p.misbehavior != nil && p.misbehavior.IsBanned(addr) {
		p.log.Info("swarmsearch.peer_ban_rejected",
			"peer", addr,
			"reason", "misbehavior score crossed threshold",
		)
		return
	}

	var (
		supported bool
		remoteID  pp.ExtensionNumber
	)
	if id, ok := hs.M[ExtensionName]; ok && id != 0 {
		supported = true
		remoteID = id
	}

	p.mu.Lock()
	ps, ok := p.peers[addr]
	if !ok {
		ps = &PeerState{Addr: addr}
		p.peers[addr] = ps
	}
	ps.Supported = supported
	ps.RemoteExtID = remoteID
	ps.SeenAt = time.Now()
	p.mu.Unlock()

	if supported {
		p.log.Info("swarmsearch.peer_capable",
			"peer", addr,
			"remote_ext_id", remoteID,
			"client", hs.V,
		)
	} else {
		p.log.Debug("swarmsearch.peer_incapable",
			"peer", addr,
			"client", hs.V,
		)
	}
}

// OnPeerClosed removes the peer from our tracking map so stale
// entries don't accumulate on long-running processes. Also
// drops the per-peer rate-limit bucket, if any, and releases
// the misbehavior entry IF the peer is not currently banned
// (ban entries are retained so a reconnect hits the block).
func (p *Protocol) OnPeerClosed(addr string) {
	p.mu.Lock()
	delete(p.peers, addr)
	p.mu.Unlock()
	if p.limiter != nil {
		p.limiter.forget(addr)
	}
	if p.misbehavior != nil {
		p.misbehavior.Forget(addr)
	}
	p.log.Debug("swarmsearch.peer_closed", "peer", addr)
}
