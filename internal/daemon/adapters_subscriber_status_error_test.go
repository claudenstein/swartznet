package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/companion"
	"github.com/swartznet/swartznet/internal/indexer"
)

type subStatusStubFetcher struct{}

func (subStatusStubFetcher) FetchCompanionTorrent(_ context.Context, _ [20]byte) (string, error) {
	return "", nil
}

type subStatusStubIngester struct{}

func (subStatusStubIngester) IndexTorrent(_ indexer.TorrentDoc) error { return nil }
func (subStatusStubIngester) IndexContent(_ indexer.ContentDoc) error { return nil }

type errPointerGetter struct{ err error }

func (g errPointerGetter) GetInfohashPointer(_ context.Context, _ [32]byte, _ []byte) ([20]byte, error) {
	return [20]byte{}, g.err
}

// TestCompanionAdapterSubscriberStatusReportsLastError covers
// the `res.Err != nil → row.LastError` branch of
// SubscriberStatus. Build a subscriber whose getter always
// fails, run one sync pass via Start (which calls runOnce
// immediately), then assert SubscriberStatus surfaces the error.
func TestCompanionAdapterSubscriberStatusReportsLastError(t *testing.T) {
	t.Parallel()
	sub, err := companion.NewSubscriber(
		errPointerGetter{err: errors.New("simulated DHT failure")},
		subStatusStubFetcher{}, subStatusStubIngester{},
		companion.SubscriberOptions{Interval: time.Hour},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	w, err := companion.NewSubscriberWorker(sub)
	if err != nil {
		t.Fatal(err)
	}
	var pk [32]byte
	pk[0] = 0x77
	w.Follow(pk, "broken")

	w.Start()
	defer w.Stop()

	// Wait for the initial runOnce to populate lastSync.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if w.TotalRuns() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if w.TotalRuns() == 0 {
		t.Fatal("worker never ran runOnce")
	}

	a := newCompanionAdapter(nil, w, "")
	rows := a.SubscriberStatus()
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	if rows[0].LastError == "" {
		t.Errorf("LastError should be populated after a failed sync, got %+v", rows[0])
	}
}
