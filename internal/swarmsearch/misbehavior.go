// Package swarmsearch implements Layer S: the sn_search BEP-10 (LTEP) peer-wire
// extension. Two SwartzNet peers negotiate sn_search in the LTEP `m` dict and
// exchange scoped search queries/results over an ordinary piece-transfer
// connection; a vanilla client sees only an unknown name in the `m` dict and
// ignores it. This package owns the protocol logic ONLY — it never imports
// Bleve (it answers via an injected LocalSearcher) and never imports the
// anacrolix torrent package (the engine injects the transport). The wire
// envelope lives in contracts/ltepwire.
package swarmsearch

import (
	"sync"
	"time"
)

// Misbehavior score constants (Bitcoin-Core-style discouragement). Points
// accumulate per peer; at BanThreshold the peer is banned for BanDuration.
// Ban state is strictly LOCAL — never gossiped, never persisted.
const (
	ScoreBadBencode        = 20 // unparseable / wrong-shape frame
	ScoreBadRecordSig      = 20 // reserved for Slice 8 (sync records)
	ScoreMalformedResult   = 10 // semantically malformed result
	ScoreStaleTxID         = 10 // result/reject for no live pending query
	ScoreUnexpectedMessage = 10 // result from an un-asked peer / unknown msg_type
	ScoreRateLimited       = 5  // query over the token-bucket rate
	ScoreQueryTooBroad     = 5  // query text under 2 chars
)

// BanThreshold is the score at which a peer is banned (matches Bitcoin Core's
// DISCOURAGEMENT_THRESHOLD). BanDuration is how long the ban holds.
const (
	BanThreshold = 100
	BanDuration  = 24 * time.Hour
)

type banEntry struct {
	score       int
	bannedUntil time.Time
}

// banman tracks per-peer misbehavior. It is safe for concurrent use.
type banman struct {
	mu  sync.Mutex
	now func() time.Time // injectable for tests
	m   map[string]*banEntry
}

func newBanman() *banman {
	return &banman{now: time.Now, m: make(map[string]*banEntry)}
}

// Add charges pts against addr. It returns true iff this charge crossed the
// ban threshold (a fresh ban). Non-positive pts is a no-op.
func (b *banman) Add(addr string, pts int) bool {
	if pts <= 0 {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.m[addr]
	if e == nil {
		e = &banEntry{}
		b.m[addr] = e
	}
	e.score += pts
	if e.score >= BanThreshold && e.bannedUntil.IsZero() {
		e.bannedUntil = b.now().Add(BanDuration)
		return true
	}
	return false
}

// IsBanned reports whether addr is currently banned. An expired ban is cleared
// (score reset) on read, so a peer gets a clean slate after BanDuration.
func (b *banman) IsBanned(addr string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.m[addr]
	if e == nil || e.bannedUntil.IsZero() {
		return false
	}
	if b.now().Before(e.bannedUntil) {
		return true
	}
	// Ban expired — reset.
	e.score = 0
	e.bannedUntil = time.Time{}
	return false
}

// Score returns addr's current accumulated misbehavior score (test/observability).
func (b *banman) Score(addr string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if e := b.m[addr]; e != nil {
		return e.score
	}
	return 0
}

// Forget drops a clean entry on disconnect but KEEPS a banned one, so a
// reconnect still hits the block.
func (b *banman) Forget(addr string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if e := b.m[addr]; e != nil && e.bannedUntil.IsZero() {
		delete(b.m, addr)
	}
}
