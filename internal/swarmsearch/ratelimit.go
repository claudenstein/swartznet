package swarmsearch

import (
	"sync"
	"time"
)

// RateLimit describes the per-peer quota the inbound query
// handler enforces on incoming sn_search queries. Zero values
// disable limiting (the field default after DefaultCapabilities),
// which is the behavior pre-M12f.
//
// Design doc §5.4 ("Rate limiting and back-pressure") calls for
// a token bucket per peer so a single noisy peer can't flood the
// index query path. Rejected queries get a RejectRateLimited
// reply so the peer's client can back off rather than hang.
type RateLimit struct {
	// QueriesPerSecond is the steady-state rate. 0 disables
	// limiting entirely.
	QueriesPerSecond float64
	// Burst is the maximum number of queries a peer can fire in
	// quick succession before being throttled. Must be >=1 when
	// QueriesPerSecond > 0.
	Burst int
}

// DefaultRateLimit returns the production default — 5 queries/s
// steady state with a burst of 10. That lets a well-behaved peer
// run a short burst of queries without tripping the limiter, and
// caps abuse at 5/s per peer. Tuned conservatively; nothing
// stops a future operator from overriding via SetRateLimit.
func DefaultRateLimit() RateLimit {
	return RateLimit{
		QueriesPerSecond: 5.0,
		Burst:            10,
	}
}

// peerBucket is a single peer's token-bucket state. One entry
// per peer address, created lazily on first query.
type peerBucket struct {
	tokens float64   // current number of tokens (0..Burst)
	last   time.Time // last time we topped up tokens
}

// rateLimiter is the per-peer token-bucket tracker. Safe for
// concurrent use — a single inbound query handler goroutine
// should Allow() before running the Bleve search.
//
// Memory: one entry per active peer address. OnPeerClosed is the
// lifecycle hook that evicts the entry so long-running daemons
// don't leak buckets.
type rateLimiter struct {
	mu    sync.Mutex
	cfg   RateLimit
	peers map[string]*peerBucket
}

func newRateLimiter(cfg RateLimit) *rateLimiter {
	return &rateLimiter{
		cfg:   cfg,
		peers: make(map[string]*peerBucket),
	}
}

// setConfig swaps the limiter's configuration at runtime. Safe
// for concurrent use. Existing buckets are left intact — they
// continue to fill at the new rate on their next Allow() call.
func (r *rateLimiter) setConfig(cfg RateLimit) {
	r.mu.Lock()
	r.cfg = cfg
	r.mu.Unlock()
}

// Allow consumes one token from the bucket for the given peer
// address. Returns true if the query should proceed, false if
// the peer is over quota. A zero rate (cfg.QueriesPerSecond == 0)
// or zero burst disables limiting and always returns true — the
// default for back-compat tests that construct a rateLimiter
// with a zero RateLimit value.
func (r *rateLimiter) Allow(addr string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cfg.QueriesPerSecond <= 0 || r.cfg.Burst <= 0 {
		return true
	}
	now := time.Now()
	b, ok := r.peers[addr]
	if !ok {
		// First query from this peer — start full.
		b = &peerBucket{
			tokens: float64(r.cfg.Burst),
			last:   now,
		}
		r.peers[addr] = b
	} else {
		// Refill based on elapsed time.
		elapsed := now.Sub(b.last).Seconds()
		if elapsed > 0 {
			b.tokens += elapsed * r.cfg.QueriesPerSecond
			if b.tokens > float64(r.cfg.Burst) {
				b.tokens = float64(r.cfg.Burst)
			}
			b.last = now
		}
	}
	if b.tokens >= 1.0 {
		b.tokens -= 1.0
		return true
	}
	return false
}

// forget drops the per-peer state for the given address. Called
// from Protocol.OnPeerClosed so closed connections do not leave
// bucket entries behind.
func (r *rateLimiter) forget(addr string) {
	r.mu.Lock()
	delete(r.peers, addr)
	r.mu.Unlock()
}

// knownPeerCount returns the number of peer buckets currently
// held. Used by TestRateLimiter to assert eviction works.
func (r *rateLimiter) knownPeerCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.peers)
}
