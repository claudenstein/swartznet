package swarmsearch

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

// TestFeelerOnceNilBookNoop covers the defensive `p.book == nil
// → return` branch of feelerOnce. Production wires a PeerBook in
// New(); construct a stripped-down Protocol to hit the guard.
func TestFeelerOnceNilBookNoop(t *testing.T) {
	t.Parallel()
	p := &Protocol{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	// Must not panic on nil PeerBook.
	p.feelerOnce(context.Background())
}
