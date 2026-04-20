package swarmsearch_test

import (
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// failingSearcher always returns an error from SearchLocal so we
// can drive handleQuery's "local error → reject" branch.
type failingSearcher struct{ err error }

func (f failingSearcher) SearchLocal(_ string, _ int) (int, []swarmsearch.LocalHit, error) {
	return 0, nil, f.err
}

// TestHandleQuerySearchErrorReplyReject covers the
// `searcher.SearchLocal returns err → sendReject(local_error)`
// branch of handleQuery. The other paths (rate limit, searcher
// disabled, query too short, happy) are covered by the existing
// handle_query_test.go.
func TestHandleQuerySearchErrorReplyReject(t *testing.T) {
	t.Parallel()
	p := swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.SetSearcher(failingSearcher{err: errors.New("bleve internal failure")})

	body, err := swarmsearch.EncodeQuery(swarmsearch.Query{
		TxID:  42,
		Q:     "ubuntu", // long enough to pass the short-query check
		Limit: 5,
	})
	if err != nil {
		t.Fatal(err)
	}

	var got []byte
	reply := func(b []byte) error {
		got = append([]byte(nil), b...)
		return nil
	}
	p.HandleMessage("1.2.3.4:6881", body, reply)

	if len(got) == 0 {
		t.Fatal("reply was not invoked — handleQuery must always send a reject on local error")
	}
	// The reply body must decode as a Reject with the local-error code.
	rj, err := swarmsearch.DecodeReject(got)
	if err != nil {
		t.Fatalf("DecodeReject: %v", err)
	}
	if rj.Code != swarmsearch.RejectTooExpensive {
		t.Errorf("Code = %d, want RejectTooExpensive (%d)", rj.Code, swarmsearch.RejectTooExpensive)
	}
}
