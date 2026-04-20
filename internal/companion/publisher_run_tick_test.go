package companion

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/indexer"
)

// TestPublisherRunRefreshTickFires covers the
// `<-tick.C → p.refreshOnce(ctx)` branch of Publisher.run.
// Build a Publisher with a very short Interval so the ticker
// fires while the test is waiting; the empty-index case ensures
// each refreshOnce call lands a recordFailure that bumps the
// publisher state's TotalAttempts counter, which we observe.
func TestPublisherRunRefreshTickFires(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "tick.bleve"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	p, err := NewPublisher(idx,
		nopPointerPutter{},
		nopTorrentSeeder{},
		PublisherOptions{
			Dir:      t.TempDir(),
			Interval: 50 * time.Millisecond,
		},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	p.Start()
	defer p.Stop()

	// Wait for the initial refreshOnce so LastRefresh is set.
	var first time.Time
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ts := p.Status().LastRefresh; !ts.IsZero() {
			first = ts
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if first.IsZero() {
		t.Fatal("initial refreshOnce never ran")
	}

	// Wait for at least one tick-driven refresh that advances
	// LastRefresh past `first`.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p.Status().LastRefresh.After(first) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("tick never fired refreshOnce; LastRefresh stayed at %v", first)
}
