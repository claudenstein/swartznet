package swarmsearch_test

import (
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

// nopSearcher always returns no hits — enough to make handleQuery
// reach the short-query check after passing the searcher==nil
// guard.
type nopSearcher struct{}

func (nopSearcher) SearchLocal(_ string, _ int) (int, []swarmsearch.LocalHit, error) {
	return 0, nil, nil
}
