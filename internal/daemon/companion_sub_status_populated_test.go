package daemon

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/companion"
	"github.com/swartznet/swartznet/internal/indexer"
)

// scriptedPointerGetter returns a fixed pointer for any
// (pubkey, salt) — enough for one sync to advance through
// the fetcher + ingester.
type scriptedPointerGetter struct {
	pointer [20]byte
}

func (s scriptedPointerGetter) GetInfohashPointer(_ context.Context, _ [32]byte, _ []byte) ([20]byte, error) {
	return s.pointer, nil
}

// pathFetcher returns a fixed on-disk path that the subscriber
// then opens to decode the companion JSON payload.
type pathFetcher struct{ path string }

func (p pathFetcher) FetchCompanionTorrent(_ context.Context, _ [20]byte) (string, error) {
	return p.path, nil
}

// recordingIngester counts IndexTorrent / IndexContent calls.
type recordingIngester struct {
	mu       sync.Mutex
	torrents int
	contents int
}

func (r *recordingIngester) IndexTorrent(_ indexer.TorrentDoc) error {
	r.mu.Lock()
	r.torrents++
	r.mu.Unlock()
	return nil
}
func (r *recordingIngester) IndexContent(_ indexer.ContentDoc) error {
	r.mu.Lock()
	r.contents++
	r.mu.Unlock()
	return nil
}

// writeCompanionPayload writes a tiny gzipped JSON companion
// index to a tempfile and returns its path. The payload has one
// torrent so the SyncResult.TorrentsImported is non-zero.
func writeCompanionPayload(t *testing.T) string {
	t.Helper()
	idx := companion.CompanionIndex{
		Version:     1,
		Format:      "swartznet-content-index",
		Publisher:   "abcd",
		GeneratedAt: time.Now().Unix(),
		Torrents: []companion.TorrentRecord{
			{InfoHash: "1111111111111111111111111111111111111111", Name: "ubuntu"},
		},
	}
	body, err := json.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "companion.json.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	if _, err := gw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestCompanionAdapterSubscriberStatusPopulatedRowFields drives
// a real Subscriber through one sync (Follow + Start + wait)
// so SubscriberStatus emits a row whose GeneratedAt > 0,
// PointerInfoHash != zero, and LastSyncAt non-zero — covering
// the previously-uncovered fill-in branches.
func TestCompanionAdapterSubscriberStatusPopulatedRowFields(t *testing.T) {
	t.Parallel()
	payload := writeCompanionPayload(t)

	getter := scriptedPointerGetter{pointer: [20]byte{0xab, 0xcd}}
	fetcher := pathFetcher{path: payload}
	rec := &recordingIngester{}
	opts := companion.DefaultSubscriberOptions()
	opts.Interval = time.Hour // we drive via Follow trigger
	sub, err := companion.NewSubscriber(getter, fetcher, rec, opts,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	w, err := companion.NewSubscriberWorker(sub)
	if err != nil {
		t.Fatal(err)
	}
	w.Start()
	defer w.Stop()

	var pub [32]byte
	pub[0] = 0x42
	w.Follow(pub, "happy-publisher")

	// Wait for the sync to land.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if w.LastSync(pub).GeneratedAt > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	a := newCompanionAdapter(nil, w, "")
	rows := a.SubscriberStatus()
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.GeneratedAt <= 0 {
		t.Errorf("GeneratedAt = %d, want > 0", row.GeneratedAt)
	}
	if row.PointerInfoHash == "" {
		t.Error("PointerInfoHash should be populated after a successful sync")
	}
	if row.LastSyncAt.IsZero() {
		t.Error("LastSyncAt should be populated after a successful sync")
	}
}
