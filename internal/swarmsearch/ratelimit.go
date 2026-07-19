package swarmsearch

import (
	"sync"
	"time"
)

// RateLimit configures the per-peer inbound-query token bucket. Zero
// QueriesPerSecond OR zero Burst disables limiting (always allow).
type RateLimit struct {
	QueriesPerSecond float64
	Burst            int
}

// DefaultRateLimit is 5 queries/sec, burst 10.
func DefaultRateLimit() RateLimit { return RateLimit{QueriesPerSecond: 5.0, Burst: 10} }

type bucket struct {
	tokens float64
	last   time.Time
}

// rateLimiter is a per-peer token bucket, keyed by address. Buckets are lazily
// created full (tokens = Burst).
type rateLimiter struct {
	mu      sync.Mutex
	now     func() time.Time // injectable for tests
	cfg     RateLimit
	buckets map[string]*bucket
}

func newRateLimiter(cfg RateLimit) *rateLimiter {
	return &rateLimiter{now: time.Now, cfg: cfg, buckets: make(map[string]*bucket)}
}

// Allow consumes one token for addr, returning whether the query is admitted.
// A disabled limiter (zero qps or burst) always allows.
func (r *rateLimiter) Allow(addr string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cfg.QueriesPerSecond <= 0 || r.cfg.Burst <= 0 {
		return true
	}
	now := r.now()
	b := r.buckets[addr]
	if b == nil {
		// Lazily created FULL so a fresh peer gets its whole burst.
		b = &bucket{tokens: float64(r.cfg.Burst), last: now}
		r.buckets[addr] = b
	} else {
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

// setConfig swaps the limiter config, keeping existing buckets.
func (r *rateLimiter) setConfig(cfg RateLimit) {
	r.mu.Lock()
	r.cfg = cfg
	r.mu.Unlock()
}

// forget drops a peer's bucket on disconnect.
func (r *rateLimiter) forget(addr string) {
	r.mu.Lock()
	delete(r.buckets, addr)
	r.mu.Unlock()
}
