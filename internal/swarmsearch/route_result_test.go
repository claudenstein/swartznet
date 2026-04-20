package swarmsearch

import (
	"io"
	"log/slog"
	"testing"
)

// TestRouteResultChannelFullDropped covers the select-default
// branch of routeResult — when the pending query's results
// channel is full, the result is dropped rather than blocking
// the read loop. The stale-txid branch is exercised by other
// tests; only the buffer-full path was missing.
func TestRouteResultChannelFullDropped(t *testing.T) {
	t.Parallel()
	p := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	pend := &pendingQuery{
		txid:    77,
		results: make(chan incomingResult, 1),
	}
	p.registerPending(pend)
	defer p.releasePending(77)

	// First call fills the buffer.
	p.routeResult("1.1.1.1:6881", Result{TxID: 77})
	// Second must hit select-default and drop.
	p.routeResult("2.2.2.2:6881", Result{TxID: 77})

	got := 0
drainLoop:
	for {
		select {
		case <-pend.results:
			got++
		default:
			break drainLoop
		}
	}
	if got != 1 {
		t.Errorf("delivered = %d, want 1 (second should drop)", got)
	}
}
