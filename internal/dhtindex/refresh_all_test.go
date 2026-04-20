package dhtindex

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
)

// recordingPutter satisfies the Putter interface and counts
// how many Put calls landed.
type recordingPutter struct {
	calls atomic.Int64
}

func (r *recordingPutter) Put(_ context.Context, _ []byte, _ KeywordValue) error {
	r.calls.Add(1)
	return nil
}

// TestRefreshAllRunsPublishOnePerKeyword exercises the previously
// 0%-covered refreshAll path. Build a manifest with two keywords,
// then call refreshAll directly: Putter.Put should fire twice.
func TestRefreshAllRunsPublishOnePerKeyword(t *testing.T) {
	t.Parallel()
	mf, _ := LoadOrCreateManifest("")
	if _, err := mf.AddHit("ubuntu", KeywordHit{IH: bytes.Repeat([]byte{1}, 20), N: "u"}); err != nil {
		t.Fatal(err)
	}
	if _, err := mf.AddHit("debian", KeywordHit{IH: bytes.Repeat([]byte{2}, 20), N: "d"}); err != nil {
		t.Fatal(err)
	}

	put := &recordingPutter{}
	p := NewPublisher(put, mf, PublisherOptions{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	p.refreshAll(context.Background())

	if got := put.calls.Load(); got != 2 {
		t.Errorf("Put calls = %d, want 2 (one per keyword)", got)
	}
}

// TestRefreshAllShortCircuitsOnStop pins the early-out behaviour:
// if stopCh is already closed, refreshAll returns at the first
// keyword without firing any Put.
func TestRefreshAllShortCircuitsOnStop(t *testing.T) {
	t.Parallel()
	mf, _ := LoadOrCreateManifest("")
	if _, err := mf.AddHit("ubuntu", KeywordHit{IH: bytes.Repeat([]byte{1}, 20), N: "u"}); err != nil {
		t.Fatal(err)
	}

	put := &recordingPutter{}
	p := NewPublisher(put, mf, PublisherOptions{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	close(p.stopCh) // pre-close so the select hits stopCh first

	p.refreshAll(context.Background())

	if got := put.calls.Load(); got != 0 {
		t.Errorf("Put calls = %d, want 0 (refreshAll should short-circuit)", got)
	}
}
