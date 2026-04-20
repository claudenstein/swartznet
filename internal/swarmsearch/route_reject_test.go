package swarmsearch

import (
	"io"
	"log/slog"
	"testing"
)

// TestRouteRejectStaleTxIDDropped covers the
// `pend == nil → return` branch of routeReject — a Reject
// with a txid that has no pending query (typically because it
// already timed out) is silently dropped.
func TestRouteRejectStaleTxIDDropped(t *testing.T) {
	t.Parallel()
	p := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	// Without registering anything, every txid is stale.
	p.routeReject("1.2.3.4:6881", Reject{TxID: 9999})
	// No assertion needed — the function must just not panic.
}

// TestRouteRejectChannelFullDropped covers the select-default
// branch: the pending query's results channel is full, so the
// reject is dropped rather than blocking the caller.
func TestRouteRejectChannelFullDropped(t *testing.T) {
	t.Parallel()
	p := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	pend := &pendingQuery{
		txid:    42,
		results: make(chan incomingResult, 1), // capacity 1
	}
	p.registerPending(pend)
	defer p.releasePending(42)

	// First call fills the channel.
	p.routeReject("1.2.3.4:6881", Reject{TxID: 42})
	// Second call must hit the select-default and drop.
	p.routeReject("1.2.3.4:6881", Reject{TxID: 42})

	// One reject must have been delivered, the second dropped.
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
