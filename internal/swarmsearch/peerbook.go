package swarmsearch

import (
	"sync"
	"time"
)

// PeerBook is a two-table peer tracker inspired by Bitcoin
// Core's AddrMan (src/addrman.cpp). Peers start in the "new"
// table when they first handshake sn_search, and are promoted
// to "tried" the first time they successfully respond to a
// query. The Protocol's outbound Query fan-out preferentially
// targets tried peers so that:
//
//   - Eclipse attacks are harder: a Sybil that advertises
//     sn_search but never answers correctly stays in "new"
//     forever and is rarely queried.
//   - Query quality is higher: tried peers have a demonstrated
//     track record of correct protocol behavior.
//
// Invariants from Bitcoin Core that apply here:
//
//   - Promotion is on OBSERVED BEHAVIOR, not on claimed
//     capability. Advertising sn_search in the LTEP handshake
//     is not sufficient to be trusted — a correct response to
//     a real query is.
//   - The tried table is bounded so a well-behaved peer doesn't
//     get crowded out by new arrivals. Eviction from tried
//     follows a least-recently-queried policy; from new follows
//     oldest-seen.
//
// This v1 implementation does NOT include ASN-based bucket
// diversity or per-node salt hashing (the asmap.dat integration
// is a post-v1 follow-up per docs/10-bitcoin-lessons.md §2).
// The tried/new split alone provides the core behavioral
// benefit — the bucket-diversity layer makes the split harder
// to game.
//
// Concurrent-safe.
type PeerBook struct {
	mu       sync.RWMutex
	tried    map[string]*BookEntry // addr → entry
	newPeers map[string]*BookEntry // addr → entry
	maxTried int
	maxNew   int
}

// BookEntry is one row in the PeerBook. Tracks the minimal
// state needed to decide promotion and fan-out selection.
type BookEntry struct {
	// Addr is the peer's "ip:port" key.
	Addr string
	// FirstSeen is when this entry was created (in either table).
	FirstSeen time.Time
	// PromotedAt is when the entry moved from new → tried.
	// Zero for entries still in the new table.
	PromotedAt time.Time
	// Successes is the count of queries this peer responded to
	// correctly. Only incremented on promotion or for already-
	// tried peers.
	Successes int
	// Failures is the count of query timeouts or rejects from
	// this peer. A high failure rate on a tried peer demotes it
	// back to new (not implemented in v1 — reserved for v1.1
	// with automatic demotion).
	Failures int
	// LastQueried is the most recent time we sent a query to
	// this peer. Used for feeler cooldown and eviction priority.
	LastQueried time.Time
}

// DefaultPeerBookLimits returns reasonable default table sizes
// for a desktop SwartzNet node. Tried is smaller than new
// because most peers in a torrent swarm never get queried
// successfully — the tried set is the high-quality core.
const (
	DefaultMaxTried = 256
	DefaultMaxNew   = 1024
)

// NewPeerBook creates an empty PeerBook with the given table
// sizes. Pass DefaultMaxTried / DefaultMaxNew for production.
func NewPeerBook(maxTried, maxNew int) *PeerBook {
	if maxTried <= 0 {
		maxTried = DefaultMaxTried
	}
	if maxNew <= 0 {
		maxNew = DefaultMaxNew
	}
	return &PeerBook{
		tried:    make(map[string]*BookEntry),
		newPeers: make(map[string]*BookEntry),
		maxTried: maxTried,
		maxNew:   maxNew,
	}
}

// AddNew inserts a peer into the new table. Called from
// OnRemoteHandshake when a peer advertises sn_search for the
// first time. If the peer is already in either table, this is
// a no-op (we don't demote tried peers back to new).
func (b *PeerBook) AddNew(addr string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.tried[addr]; ok {
		return // already tried — don't demote
	}
	if _, ok := b.newPeers[addr]; ok {
		return // already known
	}
	// Evict the oldest entry if we're at capacity.
	if len(b.newPeers) >= b.maxNew {
		b.evictOldestNew()
	}
	b.newPeers[addr] = &BookEntry{
		Addr:      addr,
		FirstSeen: time.Now(),
	}
}

// Promote moves a peer from new → tried. Called when the peer
// responds correctly to a query for the first time. If the
// peer is already tried, increments its Successes count. If
// the peer is not in either table, does nothing.
func (b *PeerBook) Promote(addr string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if e, ok := b.tried[addr]; ok {
		e.Successes++
		e.LastQueried = time.Now()
		return
	}
	e, ok := b.newPeers[addr]
	if !ok {
		return
	}
	delete(b.newPeers, addr)
	e.PromotedAt = time.Now()
	e.Successes = 1
	e.LastQueried = time.Now()
	// Evict the least-recently-queried tried entry if at cap.
	if len(b.tried) >= b.maxTried {
		b.evictLRQTried()
	}
	b.tried[addr] = e
}

// RecordFailure increments the Failures count for a peer in
// either table. Used by the query path when a peer times out
// or sends a reject. v1 does not automatically demote tried
// peers — the Failures counter is reserved for the v1.1
// automatic demotion heuristic.
func (b *PeerBook) RecordFailure(addr string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if e, ok := b.tried[addr]; ok {
		e.Failures++
		return
	}
	if e, ok := b.newPeers[addr]; ok {
		e.Failures++
	}
}

// Remove drops a peer from both tables. Called from
// OnPeerClosed.
func (b *PeerBook) Remove(addr string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.tried, addr)
	delete(b.newPeers, addr)
}

// TriedAddrs returns the addresses of every peer in the tried
// table. These are the primary targets for Query fan-out.
func (b *PeerBook) TriedAddrs() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]string, 0, len(b.tried))
	for addr := range b.tried {
		out = append(out, addr)
	}
	return out
}

// NewAddrs returns the addresses of every peer in the new
// table. Used for feeler queries (the subset of fan-out that
// tests untried peers).
func (b *PeerBook) NewAddrs() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]string, 0, len(b.newPeers))
	for addr := range b.newPeers {
		out = append(out, addr)
	}
	return out
}

// TriedCount returns the number of peers in the tried table.
func (b *PeerBook) TriedCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.tried)
}

// NewCount returns the number of peers in the new table.
func (b *PeerBook) NewCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.newPeers)
}

// IsTried reports whether the given peer is in the tried table.
func (b *PeerBook) IsTried(addr string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, ok := b.tried[addr]
	return ok
}

// evictOldestNew removes the oldest entry from the new table.
// Caller must hold mu.
func (b *PeerBook) evictOldestNew() {
	var oldest string
	var oldestTime time.Time
	for addr, e := range b.newPeers {
		if oldest == "" || e.FirstSeen.Before(oldestTime) {
			oldest = addr
			oldestTime = e.FirstSeen
		}
	}
	if oldest != "" {
		delete(b.newPeers, oldest)
	}
}

// evictLRQTried removes the least-recently-queried entry from
// the tried table. Caller must hold mu.
func (b *PeerBook) evictLRQTried() {
	var lrq string
	var lrqTime time.Time
	for addr, e := range b.tried {
		if lrq == "" || e.LastQueried.Before(lrqTime) {
			lrq = addr
			lrqTime = e.LastQueried
		}
	}
	if lrq != "" {
		delete(b.tried, lrq)
	}
}
