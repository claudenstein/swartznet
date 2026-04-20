package swarmsearch

import (
	"io"
	"log/slog"
	"testing"
)

// TestChargeMisbehaviorNilTrackerNoop covers the
// `p.misbehavior == nil → return` defensive branch. Production
// always wires a tracker via New(), so the guard is unreachable
// from integration tests; construct a stripped-down Protocol to
// exercise it.
func TestChargeMisbehaviorNilTrackerNoop(t *testing.T) {
	t.Parallel()
	p := &Protocol{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	// Must not panic on nil misbehavior tracker.
	p.chargeMisbehavior("1.2.3.4:6881", 50, "test")
}
