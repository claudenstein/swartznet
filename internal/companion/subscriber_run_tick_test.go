package companion

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/indexer"
)

type subTickIngester struct{}

func (subTickIngester) IndexTorrent(_ indexer.TorrentDoc) error { return nil }
func (subTickIngester) IndexContent(_ indexer.ContentDoc) error { return nil }

type subTickGetter struct{}

func (subTickGetter) GetInfohashPointer(_ context.Context, _ [32]byte, _ []byte) ([20]byte, error) {
	return [20]byte{}, context.Canceled
}

type subTickFetcher struct{}

func (subTickFetcher) FetchCompanionTorrent(_ context.Context, _ [20]byte) (string, error) {
	return "", nil
}

// TestSubscriberRunRefreshTickFires covers the
// `<-tick.C → w.runOnce(ctx)` branch of SubscriberWorker.run.
// Use a 50ms Interval and at least one Follow so the tick
// reliably advances the worker's TotalRuns counter.
func TestSubscriberRunRefreshTickFires(t *testing.T) {
	t.Parallel()
	sub, err := NewSubscriber(subTickGetter{}, subTickFetcher{}, subTickIngester{},
		SubscriberOptions{Interval: 50 * time.Millisecond},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewSubscriberWorker(sub)
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately do NOT call Follow — Follow fires the trigger
	// channel which would steal the second runOnce slot from the
	// ticker. With no follows, the trigger stays empty and the
	// only path that bumps TotalRuns past 1 is the tick.
	w.Start()
	defer w.Stop()

	// Initial runOnce + at least one tick-driven runOnce.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if w.TotalRuns() >= 2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("TotalRuns = %d, want >= 2 (initial + tick)", w.TotalRuns())
}
