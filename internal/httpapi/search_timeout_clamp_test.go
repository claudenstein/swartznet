package httpapi

import (
	"testing"
	"time"
)

// TestClampSearchTimeout pins the client-timeout clamp used by
// handleSearch for both SwarmTimeoutMs and DHTTimeoutMs. Before the
// fix an arbitrarily large client value flowed straight through and
// could pin a query (and its handler goroutine) open far past any
// sane budget.
func TestClampSearchTimeout(t *testing.T) {
	t.Parallel()
	const def = 2 * time.Second
	tests := []struct {
		name string
		ms   int
		want time.Duration
	}{
		{name: "zero uses default", ms: 0, want: def},
		{name: "negative uses default", ms: -1, want: def},
		{name: "in-range value passes through", ms: 1500, want: 1500 * time.Millisecond},
		{name: "exactly at ceiling passes through", ms: int(maxSearchTimeout / time.Millisecond), want: maxSearchTimeout},
		{name: "above ceiling is clamped", ms: 999_999_999, want: maxSearchTimeout},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := clampSearchTimeout(tc.ms, def); got != tc.want {
				t.Errorf("clampSearchTimeout(%d) = %v, want %v", tc.ms, got, tc.want)
			}
		})
	}
}
