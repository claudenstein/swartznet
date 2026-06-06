package dhtindex

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

// failingPutter returns a fixed error from Put, simulating a put that
// reached zero DHT nodes (the AnacrolixPutter.Put zero-node guard
// surfaces exactly this as an error).
type failingPutter struct {
	calls int
	err   error
}

func (f *failingPutter) Put(_ context.Context, _ []byte, _ KeywordValue) error {
	f.calls++
	return f.err
}

// TestPublishOneFailedPutDoesNotAdvanceLastPublished verifies the
// fail-closed property behind the zero-node fix: when Put fails (e.g.
// because it reached zero DHT nodes), publishOne must MarkFailed and
// must NOT advance LastPublished. Otherwise the MinPutInterval rate
// limiter would suppress the retry while the keyword never landed.
func TestPublishOneFailedPutDoesNotAdvanceLastPublished(t *testing.T) {
	t.Parallel()
	mf, _ := LoadOrCreateManifest("")
	if _, err := mf.AddHit("ubuntu", KeywordHit{IH: bytes.Repeat([]byte{1}, 20), N: "u"}); err != nil {
		t.Fatal(err)
	}

	put := &failingPutter{err: errors.New("dhtindex: put reached zero DHT nodes")}
	p := NewPublisher(put, mf, PublisherOptions{MinPutInterval: time.Hour},
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	p.publishOne(context.Background(), "ubuntu")

	entry := mf.Entries["ubuntu"]
	if entry == nil {
		t.Fatal("manifest entry missing")
	}
	if !entry.LastPublished.IsZero() {
		t.Errorf("LastPublished advanced on a failed put: %v", entry.LastPublished)
	}
	if entry.PublishCount != 0 {
		t.Errorf("PublishCount = %d, want 0 on failed put", entry.PublishCount)
	}
	if entry.LastError == "" {
		t.Error("LastError empty; want the put failure recorded")
	}

	// Because LastPublished stayed zero, the rate-limiter must NOT
	// suppress the next attempt — the retry must reach the Putter.
	p.publishOne(context.Background(), "ubuntu")
	if put.calls != 2 {
		t.Errorf("Put calls = %d, want 2 (failed put must not block retry)", put.calls)
	}
}

// TestPublishOneSuccessThenThrottles is the contrast case: a
// successful put advances LastPublished, after which the rate limiter
// suppresses an immediate re-publish of the same keyword.
func TestPublishOneSuccessThenThrottles(t *testing.T) {
	t.Parallel()
	mf, _ := LoadOrCreateManifest("")
	if _, err := mf.AddHit("ubuntu", KeywordHit{IH: bytes.Repeat([]byte{1}, 20), N: "u"}); err != nil {
		t.Fatal(err)
	}

	put := &failingPutter{err: nil} // succeeds
	p := NewPublisher(put, mf, PublisherOptions{MinPutInterval: time.Hour},
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	p.publishOne(context.Background(), "ubuntu")
	if mf.Entries["ubuntu"].LastPublished.IsZero() {
		t.Fatal("LastPublished not advanced after a successful put")
	}
	// Immediate re-publish must be throttled (no second Put).
	p.publishOne(context.Background(), "ubuntu")
	if put.calls != 1 {
		t.Errorf("Put calls = %d, want 1 (second call should be throttled)", put.calls)
	}
}
