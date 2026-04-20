package companion

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/swartznet/swartznet/internal/indexer"
)

// counterIngester counts IndexTorrent calls so we can verify
// runOnce returns *before* invoking Sync (which would land at
// least one IndexTorrent on the stub publisher's data).
type counterIngester struct{ calls atomic.Int64 }

func (c *counterIngester) IndexTorrent(_ indexer.TorrentDoc) error {
	c.calls.Add(1)
	return nil
}
func (c *counterIngester) IndexContent(_ indexer.ContentDoc) error {
	c.calls.Add(1)
	return nil
}

type panicGetter struct{}

func (panicGetter) GetInfohashPointer(_ context.Context, _ [32]byte, _ []byte) ([20]byte, error) {
	panic("getter must not be called when stopCh is closed")
}

type panicFetcher struct{}

func (panicFetcher) FetchCompanionTorrent(_ context.Context, _ [20]byte) (string, error) {
	panic("fetcher must not be called when stopCh is closed")
}

// TestRunOnceShortCircuitsOnStop covers the
// `<-w.stopCh → return` mid-loop branch of runOnce. With a
// followed publisher in the worker's follow list and stopCh
// pre-closed, runOnce must hit the stop case on the first
// iteration and return without calling Subscriber.Sync (which
// would invoke the panic-getter).
func TestRunOnceShortCircuitsOnStop(t *testing.T) {
	t.Parallel()
	sub, err := NewSubscriber(panicGetter{}, panicFetcher{}, &counterIngester{},
		DefaultSubscriberOptions(),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewSubscriberWorker(sub)
	if err != nil {
		t.Fatal(err)
	}
	var pub [32]byte
	pub[0] = 0x99
	w.Follow(pub, "test")

	// Pre-close stopCh so the loop's select hits stopCh on the
	// very first iteration.
	close(w.stopCh)

	// Must not panic — Sync is never reached.
	w.runOnce(context.Background())

	// The early return skips the post-loop totalRuns++ — assert
	// that and that lastSync was not populated for the followed pub.
	if w.totalRuns != 0 {
		t.Errorf("totalRuns = %d, want 0 (early return skips counter)", w.totalRuns)
	}
	if _, ok := w.lastSync[pub]; ok {
		t.Errorf("lastSync should be untouched on early return")
	}
}
