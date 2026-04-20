package swarmsearch

import (
	"io"
	"log/slog"
	"testing"
)

// TestMisbehaviorScoreNilTrackerReturnsZero covers the
// `p.misbehavior == nil` early-return branch — production code
// always wires a tracker via New(), but the helper is meant to
// be defensive against that field being nil. Manually build a
// stripped-down Protocol so we can hit the nil branch.
func TestMisbehaviorScoreNilTrackerReturnsZero(t *testing.T) {
	t.Parallel()
	p := &Protocol{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if got := p.MisbehaviorScore("1.2.3.4:6881"); got != 0 {
		t.Errorf("MisbehaviorScore on nil tracker = %d, want 0", got)
	}
}

// TestIsBannedNilTrackerReturnsFalse covers the
// `p.misbehavior == nil` early-return branch in IsBanned for the
// same reason.
func TestIsBannedNilTrackerReturnsFalse(t *testing.T) {
	t.Parallel()
	p := &Protocol{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if p.IsBanned("1.2.3.4:6881") {
		t.Errorf("IsBanned on nil tracker = true, want false")
	}
}

// TestSetRateLimitNilLimiterConstructsNew covers the
// `p.limiter == nil` branch — SetRateLimit must lazily create a
// limiter rather than panic. Manually build a Protocol with a
// nil limiter, then call SetRateLimit and verify a non-nil
// limiter is now in place.
func TestSetRateLimitNilLimiterConstructsNew(t *testing.T) {
	t.Parallel()
	p := &Protocol{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if p.limiter != nil {
		t.Fatal("setup: limiter should start nil")
	}
	p.SetRateLimit(RateLimit{QueriesPerSecond: 5, Burst: 5})
	if p.limiter == nil {
		t.Errorf("limiter still nil after SetRateLimit")
	}
}
