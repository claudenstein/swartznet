package companion

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/swartznet/swartznet/internal/indexer"
)

// nopPointerPutter / nopTorrentSeeder satisfy the publisher's
// collaborators without doing anything; the constructor never
// invokes them in these unit tests.
type nopPointerPutter struct{}

func (nopPointerPutter) PutInfohashPointer(_ context.Context, _ []byte, _ [20]byte) error {
	return nil
}

type nopTorrentSeeder struct{}

func (nopTorrentSeeder) AddTorrentMetaInfo(_ *metainfo.MetaInfo) (any, error) {
	return nil, nil
}

// TestRefreshNowThrottlesAfterRecentRefresh covers the
// previously-uncovered ErrTooSoon branch of RefreshNow. We
// construct a Publisher (without starting it) and forge
// lastRefresh to "just now" so the next RefreshNow sees an
// elapsed time below MinInterval.
func TestRefreshNowThrottlesAfterRecentRefresh(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "idx"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	p, err := NewPublisher(idx,
		nopPointerPutter{},
		nopTorrentSeeder{},
		PublisherOptions{
			Dir:         t.TempDir(),
			MinInterval: 1 * time.Hour, // very large so the throttle fires
		},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	// Forge lastRefresh to "just now" to trip the throttle.
	p.mu.Lock()
	p.lastRefresh = time.Now()
	p.mu.Unlock()

	if err := p.RefreshNow(); err != ErrTooSoon {
		t.Errorf("RefreshNow err = %v, want ErrTooSoon", err)
	}
}

// TestRefreshNowTriggerAlreadyQueuedNoBlock covers the default-
// case branch of the trigger send: with one trigger already in
// the buffered channel, a second RefreshNow returns nil without
// blocking and without enqueueing a duplicate.
func TestRefreshNowTriggerAlreadyQueuedNoBlock(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "idx"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	p, err := NewPublisher(idx,
		nopPointerPutter{},
		nopTorrentSeeder{},
		PublisherOptions{Dir: t.TempDir()},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	if err := p.RefreshNow(); err != nil {
		t.Fatalf("first RefreshNow: %v", err)
	}
	// Second call: trigger is already in the buffered channel
	// (nothing's draining it because Start wasn't called); the
	// `default:` branch of the select fires.
	if err := p.RefreshNow(); err != nil {
		t.Fatalf("second RefreshNow: %v", err)
	}
}
