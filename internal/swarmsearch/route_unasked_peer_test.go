package swarmsearch

import (
	"io"
	"log/slog"
	"testing"
)

// TestRouteResultFromUnaskedPeerDropped — a Result whose txid
// matches a live pending query but whose sender was never part
// of that query's fan-out must be dropped and charged. TxIDs are
// guessable (monotonic counter), so before this guard any peer
// could inject hits into someone else's in-flight query.
func TestRouteResultFromUnaskedPeerDropped(t *testing.T) {
	t.Parallel()
	p := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	pend := &pendingQuery{
		txid:    11,
		results: make(chan incomingResult, 2),
		asked:   map[string]struct{}{"1.1.1.1:6881": {}},
	}
	p.registerPending(pend)
	defer p.releasePending(11)

	// Spoofer was never asked — dropped + charged.
	p.routeResult("6.6.6.6:6881", Result{TxID: 11})
	select {
	case <-pend.results:
		t.Fatal("result from unasked peer must not be delivered")
	default:
	}
	if got := p.MisbehaviorScore("6.6.6.6:6881"); got == 0 {
		t.Error("unasked-peer result should charge misbehavior")
	}

	// The asked peer still gets through.
	p.routeResult("1.1.1.1:6881", Result{TxID: 11})
	select {
	case ir := <-pend.results:
		if ir.peer != "1.1.1.1:6881" {
			t.Errorf("delivered peer = %q, want asked peer", ir.peer)
		}
	default:
		t.Error("result from asked peer should be delivered")
	}
}

// TestRouteRejectFromUnaskedPeerDropped — same guard for Reject
// frames: only peers in the query's fan-out set may inject
// outcomes into the collector.
func TestRouteRejectFromUnaskedPeerDropped(t *testing.T) {
	t.Parallel()
	p := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	pend := &pendingQuery{
		txid:    12,
		results: make(chan incomingResult, 2),
		asked:   map[string]struct{}{"1.1.1.1:6881": {}},
	}
	p.registerPending(pend)
	defer p.releasePending(12)

	p.routeReject("6.6.6.6:6881", Reject{TxID: 12})
	select {
	case <-pend.results:
		t.Fatal("reject from unasked peer must not be delivered")
	default:
	}
	if got := p.MisbehaviorScore("6.6.6.6:6881"); got == 0 {
		t.Error("unasked-peer reject should charge misbehavior")
	}

	p.routeReject("1.1.1.1:6881", Reject{TxID: 12})
	select {
	case ir := <-pend.results:
		if ir.result.MsgType != MsgTypeReject {
			t.Errorf("delivered MsgType = %d, want MsgTypeReject", ir.result.MsgType)
		}
	default:
		t.Error("reject from asked peer should be delivered")
	}
}
