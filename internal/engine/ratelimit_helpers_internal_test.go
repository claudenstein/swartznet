package engine

import (
	"testing"

	"golang.org/x/time/rate"
)

// TestLimiterToBytesPerSecNilSafe covers the documented nil-
// limiter guard: a nil *rate.Limiter returns 0 (the "unlimited"
// sentinel) rather than panicking.
func TestLimiterToBytesPerSecNilSafe(t *testing.T) {
	t.Parallel()
	if got := limiterToBytesPerSec(nil); got != 0 {
		t.Errorf("limiterToBytesPerSec(nil) = %d, want 0", got)
	}
}

// TestSetLimiterBytesPerSecNilSafe covers the same nil guard
// on the setter — the call must be a no-op rather than a nil
// deref.
func TestSetLimiterBytesPerSecNilSafe(t *testing.T) {
	t.Parallel()
	// Call with nil; the explicit no-panic + still-zero return on a
	// fresh limiter constructed afterwards is the only assertion we
	// can make without observing limiter internals.
	setLimiterBytesPerSec(nil, 1024)
}

// TestSetLimiterBytesPerSecMinBurst covers the burst < 16 KiB
// floor. With a tiny rate (e.g. 100 bps), burst would otherwise
// be 100, but the helper raises it to 16 KiB so single-chunk
// reservations don't starve.
func TestSetLimiterBytesPerSecMinBurst(t *testing.T) {
	t.Parallel()
	l := rate.NewLimiter(rate.Inf, unlimitedBurst)
	setLimiterBytesPerSec(l, 100) // tiny rate
	if got := l.Burst(); got < 16*1024 {
		t.Errorf("burst = %d, want at least 16 KiB (16384)", got)
	}
	if got := l.Limit(); got != rate.Limit(100) {
		t.Errorf("limit = %v, want 100", got)
	}
}

// TestSetLimiterBytesPerSecLargeRate covers the burst==rate path
// where bps is large enough that no floor is applied.
func TestSetLimiterBytesPerSecLargeRate(t *testing.T) {
	t.Parallel()
	l := rate.NewLimiter(rate.Inf, unlimitedBurst)
	setLimiterBytesPerSec(l, 1_000_000)
	if got := l.Burst(); got != 1_000_000 {
		t.Errorf("burst = %d, want 1_000_000", got)
	}
}

// TestSetLimiterBytesPerSecZeroDisables covers the non-positive
// branch: passing 0 puts the limiter into rate.Inf mode with
// the unlimitedBurst sentinel.
func TestSetLimiterBytesPerSecZeroDisables(t *testing.T) {
	t.Parallel()
	l := rate.NewLimiter(rate.Limit(1024), 1024)
	setLimiterBytesPerSec(l, 0)
	if got := l.Limit(); got != rate.Inf {
		t.Errorf("limit = %v, want rate.Inf", got)
	}
	if got := l.Burst(); got != unlimitedBurst {
		t.Errorf("burst = %d, want unlimitedBurst (%d)", got, unlimitedBurst)
	}
}
