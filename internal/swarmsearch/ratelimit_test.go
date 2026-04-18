package swarmsearch

import (
	"testing"
	"time"
)

func TestRateLimiterDisabledWhenZero(t *testing.T) {
	t.Parallel()
	rl := newRateLimiter(RateLimit{})
	for i := 0; i < 1000; i++ {
		if !rl.Allow("peer:1") {
			t.Fatalf("iter %d: expected zero-config limiter to always allow", i)
		}
	}
}

func TestRateLimiterBurstAllowsThenBlocks(t *testing.T) {
	t.Parallel()
	rl := newRateLimiter(RateLimit{
		QueriesPerSecond: 1, // slow refill
		Burst:            5,
	})
	// First 5 queries must succeed (burst is 5).
	for i := 0; i < 5; i++ {
		if !rl.Allow("peer:1") {
			t.Fatalf("iter %d: first %d queries should pass", i, 5)
		}
	}
	// 6th immediately after must be blocked.
	if rl.Allow("peer:1") {
		t.Error("6th query in burst window should have been blocked")
	}
}

func TestRateLimiterRefillsOverTime(t *testing.T) {
	t.Parallel()
	// Use a fast rate so the test doesn't sleep long.
	rl := newRateLimiter(RateLimit{
		QueriesPerSecond: 100, // 10ms per token
		Burst:            2,
	})
	// Drain burst (two separate calls so the intent is unambiguous;
	// the previous combined `||` form tripped staticcheck SA4000).
	if !rl.Allow("peer:1") {
		t.Fatal("burst drain: first Allow should succeed")
	}
	if !rl.Allow("peer:1") {
		t.Fatal("burst drain: second Allow should succeed")
	}
	if rl.Allow("peer:1") {
		t.Fatal("third query should be blocked")
	}
	// Wait enough real time for at least one refill.
	time.Sleep(30 * time.Millisecond)
	if !rl.Allow("peer:1") {
		t.Error("query after refill should have been allowed")
	}
}

func TestRateLimiterIsolatesPeers(t *testing.T) {
	t.Parallel()
	rl := newRateLimiter(RateLimit{
		QueriesPerSecond: 1,
		Burst:            3,
	})
	// Drain peer A's burst.
	for i := 0; i < 3; i++ {
		if !rl.Allow("peer:a") {
			t.Fatalf("peer:a burst %d failed", i)
		}
	}
	if rl.Allow("peer:a") {
		t.Error("peer:a over-burst should have been blocked")
	}
	// Peer B must still get a full burst.
	for i := 0; i < 3; i++ {
		if !rl.Allow("peer:b") {
			t.Errorf("peer:b %d should not inherit peer:a throttling", i)
		}
	}
}

func TestRateLimiterForgetEvicts(t *testing.T) {
	t.Parallel()
	rl := newRateLimiter(RateLimit{QueriesPerSecond: 1, Burst: 1})
	rl.Allow("peer:a")
	rl.Allow("peer:b")
	if n := rl.knownPeerCount(); n != 2 {
		t.Errorf("knownPeerCount = %d, want 2", n)
	}
	rl.forget("peer:a")
	if n := rl.knownPeerCount(); n != 1 {
		t.Errorf("knownPeerCount after forget = %d, want 1", n)
	}
}

func TestRateLimiterSetConfigSwapsRate(t *testing.T) {
	t.Parallel()
	rl := newRateLimiter(RateLimit{}) // initially disabled
	for i := 0; i < 100; i++ {
		if !rl.Allow("peer:a") {
			t.Fatal("disabled limiter should always allow")
		}
	}
	// Swap in a tight burst.
	rl.setConfig(RateLimit{QueriesPerSecond: 1, Burst: 1})
	// The existing bucket entry for peer:a still has the
	// full credit from the disabled run — so the first
	// post-swap call consumes that (or tops up). Either way,
	// after a handful of calls we must see a block.
	var blocked bool
	for i := 0; i < 10; i++ {
		if !rl.Allow("peer:a") {
			blocked = true
			break
		}
	}
	if !blocked {
		t.Error("expected limiter to block after setConfig tightened the rate")
	}
}
