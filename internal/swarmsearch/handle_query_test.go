package swarmsearch_test

import (
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// TestHandleQueryRejectsWhenSearcherDisabled drives the
// previously-uncovered "searcher == nil" reject branch. The
// fresh Protocol from New() has no LocalSearcher wired, so any
// inbound query is rejected with code RejectShuttingDown.
func TestHandleQueryRejectsWhenSearcherDisabled(t *testing.T) {
	t.Parallel()
	p := swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))

	body, err := swarmsearch.EncodeQuery(swarmsearch.Query{TxID: 9, Q: "ubuntu", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}

	var got []byte
	reply := func(b []byte) error {
		got = b
		return nil
	}
	p.HandleMessage("1.2.3.4:6881", body, reply)
	if len(got) == 0 {
		t.Fatal("expected a reject reply payload, got none")
	}
	rj, err := swarmsearch.DecodeReject(got)
	if err != nil {
		t.Fatalf("DecodeReject: %v", err)
	}
	if rj.TxID != 9 {
		t.Errorf("Reject.TxID = %d, want 9", rj.TxID)
	}
}

// TestHandleQueryShortQueryCharges drives the "query too short"
// branch: the trimmed query string is < 2 chars, the peer is
// charged ScoreQueryTooBroad and a Reject is sent.
func TestHandleQueryShortQueryCharges(t *testing.T) {
	t.Parallel()
	p := swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	const peer = "1.2.3.4:6881"

	body, err := swarmsearch.EncodeQuery(swarmsearch.Query{TxID: 11, Q: "x", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}

	// Wire a no-op LocalSearcher so we get past the searcher==nil
	// guard (which fires before the short-query check).
	p.SetSearcher(nopSearcher{})
	reply := func(_ []byte) error { return nil }
	p.HandleMessage(peer, body, reply)

	if got := p.MisbehaviorScore(peer); got == 0 {
		t.Error("MisbehaviorScore should be non-zero after a too-short query")
	}
}

// TestHandleQueryReplyErrorLogged covers handleQuery's
// `if err := reply(payloadOut); err != nil { return }` arm.
// A reply closure that returns an error must be tolerated —
// handleQuery debug-logs and returns rather than panicking
// or retrying.
func TestHandleQueryReplyErrorLogged(t *testing.T) {
	t.Parallel()
	p := swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.SetSearcher(nopSearcher{})

	body, err := swarmsearch.EncodeQuery(swarmsearch.Query{TxID: 6, Q: "ubuntu", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	failingReply := func(_ []byte) error {
		return errors.New("simulated reply send failure")
	}
	p.HandleMessage("1.2.3.4:6881", body, failingReply)
}

// TestHandleQueryNilReplyDecodeOnlyMode covers the
// `if reply == nil { return }` arm of handleQuery — the
// "decode-only mode used in unit tests" comment promises that
// passing a nil reply lets the responder run through search
// without trying to send back a result. We have searched OK
// (nopSearcher returns 0/nil), encoded the result, and now
// just bail without invoking reply.
func TestHandleQueryNilReplyDecodeOnlyMode(t *testing.T) {
	t.Parallel()
	p := swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.SetSearcher(nopSearcher{})

	body, err := swarmsearch.EncodeQuery(swarmsearch.Query{TxID: 5, Q: "ubuntu", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	// Pass nil reply — handleQuery must still complete without
	// panic and without sending anything (there is nowhere to
	// send it).
	p.HandleMessage("1.2.3.4:6881", body, nil)
}

// nopSearcher always returns no hits — enough to make handleQuery
// reach the short-query check after passing the searcher==nil
// guard.
type nopSearcher struct{}

func (nopSearcher) SearchLocal(_ string, _ int) (int, []swarmsearch.LocalHit, error) {
	return 0, nil, nil
}
