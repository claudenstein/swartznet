package companion_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/companion"
	"github.com/swartznet/swartznet/internal/indexer"
)

// fakeGetter pretends to be a BEP-44 mutable-item getter.
// Calling SetPointer arms the next get; Sync is the only thing
// that observes its state.
type fakeGetter struct {
	mu       sync.Mutex
	pointer  [20]byte
	calls    int
	failWith error
	lastPub  [32]byte
	lastSalt []byte
}

func (g *fakeGetter) SetPointer(ih [20]byte) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pointer = ih
}

func (g *fakeGetter) GetInfohashPointer(ctx context.Context, pubkey [32]byte, salt []byte) ([20]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	g.lastPub = pubkey
	g.lastSalt = append([]byte(nil), salt...)
	if g.failWith != nil {
		return [20]byte{}, g.failWith
	}
	return g.pointer, nil
}

// fakeFetcher pretends to download a companion torrent. It
// just returns a precomputed on-disk path. If the test wants
// to simulate a fetch failure, set failWith.
type fakeFetcher struct {
	path     string
	failWith error
	calls    atomic.Int64
	lastIH   atomic.Pointer[[20]byte]
}

func (f *fakeFetcher) FetchCompanionTorrent(ctx context.Context, ih [20]byte) (string, error) {
	f.calls.Add(1)
	ihCopy := ih
	f.lastIH.Store(&ihCopy)
	if f.failWith != nil {
		return "", f.failWith
	}
	return f.path, nil
}

// recorderIngester is an in-memory Ingester that just records
// every IndexTorrent / IndexContent call. Used so the tests
// don't have to spin up a real Bleve index.
type recorderIngester struct {
	mu       sync.Mutex
	torrents []indexer.TorrentDoc
	contents []indexer.ContentDoc
	failNth  int // 0 = never fail; otherwise fail on the Nth call
	calls    int
}

func (r *recorderIngester) IndexTorrent(doc indexer.TorrentDoc) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.failNth > 0 && r.calls == r.failNth {
		return errors.New("simulated failure")
	}
	r.torrents = append(r.torrents, doc)
	return nil
}

func (r *recorderIngester) IndexContent(doc indexer.ContentDoc) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.failNth > 0 && r.calls == r.failNth {
		return errors.New("simulated failure")
	}
	r.contents = append(r.contents, doc)
	return nil
}

func (r *recorderIngester) snapshot() ([]indexer.TorrentDoc, []indexer.ContentDoc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := append([]indexer.TorrentDoc(nil), r.torrents...)
	c := append([]indexer.ContentDoc(nil), r.contents...)
	return t, c
}

// writeCompanionPayload synthesises a small CompanionIndex,
// encodes it via companion.Encode, writes it to a temp file, and
// returns (path, generatedAt).
func writeCompanionPayload(t *testing.T) (string, int64) {
	t.Helper()
	idx := companion.CompanionIndex{
		Publisher:   "abcd",
		GeneratedAt: time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC).Unix(),
		Torrents: []companion.TorrentRecord{
			{
				InfoHash: "1111111111111111111111111111111111111111",
				Name:     "Ubuntu 24.04",
				Size:     6 << 30,
				AddedAt:  time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC).Unix(),
				Files: []companion.FileRecord{
					{
						Index: 0, Path: "README.md", Size: 1024,
						Mime: "text/markdown", Extractor: "plaintext",
						Chunks: []companion.ContentChunk{
							{Text: "the quick brown fox"},
							{Text: "jumps over the lazy dog"},
						},
					},
				},
			},
			{
				InfoHash: "2222222222222222222222222222222222222222",
				Name:     "Debian Bookworm",
				Files: []companion.FileRecord{
					{
						Index: 0, Path: "README.txt",
						Chunks: []companion.ContentChunk{{Text: "release notes"}},
					},
				},
			},
		},
	}
	payload, err := companion.Encode(idx)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "swartznet-content-index-v1.json.gz")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, idx.GeneratedAt
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestSubscriberSyncHappyPath(t *testing.T) {
	t.Parallel()
	path, generatedAt := writeCompanionPayload(t)

	getter := &fakeGetter{}
	getter.SetPointer([20]byte{0xab, 0xcd}) // arbitrary
	fetcher := &fakeFetcher{path: path}
	rec := &recorderIngester{}

	sub, err := companion.NewSubscriber(getter, fetcher, rec, companion.DefaultSubscriberOptions(), discardLogger())
	if err != nil {
		t.Fatal(err)
	}

	var pub [32]byte
	pub[0] = 1 // arbitrary publisher key
	res := sub.Sync(context.Background(), pub)

	if res.Err != nil {
		t.Fatalf("Sync err = %v", res.Err)
	}
	if res.GeneratedAt != generatedAt {
		t.Errorf("GeneratedAt = %d, want %d", res.GeneratedAt, generatedAt)
	}
	if res.TorrentsImported != 2 {
		t.Errorf("TorrentsImported = %d, want 2", res.TorrentsImported)
	}
	if res.ContentImported != 3 {
		t.Errorf("ContentImported = %d, want 3", res.ContentImported)
	}

	gotTorrents, gotContents := rec.snapshot()
	if len(gotTorrents) != 2 {
		t.Errorf("ingested torrents = %d, want 2", len(gotTorrents))
	}
	if len(gotContents) != 3 {
		t.Errorf("ingested contents = %d, want 3", len(gotContents))
	}
	// Sanity-check that the salt the getter saw matches the
	// well-known constant.
	if string(getter.lastSalt) != companion.SaltContentIndex {
		t.Errorf("lastSalt = %q, want %q", getter.lastSalt, companion.SaltContentIndex)
	}
}

func TestSubscriberSyncPointerError(t *testing.T) {
	t.Parallel()
	path, _ := writeCompanionPayload(t)

	getter := &fakeGetter{failWith: errors.New("dht unreachable")}
	fetcher := &fakeFetcher{path: path}
	rec := &recorderIngester{}

	sub, err := companion.NewSubscriber(getter, fetcher, rec, companion.DefaultSubscriberOptions(), discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	res := sub.Sync(context.Background(), [32]byte{})
	if res.Err == nil {
		t.Fatal("expected error")
	}
	if fetcher.calls.Load() != 0 {
		t.Errorf("fetcher should not have been called when pointer fails")
	}
}

func TestSubscriberSyncFetcherError(t *testing.T) {
	t.Parallel()
	getter := &fakeGetter{}
	getter.SetPointer([20]byte{0x42})
	fetcher := &fakeFetcher{failWith: errors.New("swarm unreachable")}
	rec := &recorderIngester{}

	sub, err := companion.NewSubscriber(getter, fetcher, rec, companion.DefaultSubscriberOptions(), discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	res := sub.Sync(context.Background(), [32]byte{})
	if res.Err == nil {
		t.Fatal("expected error")
	}
	if res.PointerInfoHash[0] != 0x42 {
		t.Errorf("PointerInfoHash should still be recorded on fetcher failure")
	}
}

func TestSubscriberSyncIngestPartialFailure(t *testing.T) {
	t.Parallel()
	path, _ := writeCompanionPayload(t)

	getter := &fakeGetter{}
	getter.SetPointer([20]byte{0xab})
	fetcher := &fakeFetcher{path: path}
	// Fail on the second IndexTorrent call (the second torrent
	// after the first content was already accepted).
	rec := &recorderIngester{failNth: 4}

	sub, err := companion.NewSubscriber(getter, fetcher, rec, companion.DefaultSubscriberOptions(), discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	res := sub.Sync(context.Background(), [32]byte{})
	if res.Err == nil {
		t.Fatal("expected ingest error")
	}
	if res.TorrentsImported != 1 {
		t.Errorf("TorrentsImported = %d, want 1", res.TorrentsImported)
	}
}

func TestNewSubscriberValidatesArgs(t *testing.T) {
	t.Parallel()
	getter := &fakeGetter{}
	fetcher := &fakeFetcher{}
	rec := &recorderIngester{}
	if _, err := companion.NewSubscriber(nil, fetcher, rec, companion.DefaultSubscriberOptions(), discardLogger()); err == nil {
		t.Error("expected error for nil getter")
	}
	if _, err := companion.NewSubscriber(getter, nil, rec, companion.DefaultSubscriberOptions(), discardLogger()); err == nil {
		t.Error("expected error for nil fetcher")
	}
	if _, err := companion.NewSubscriber(getter, fetcher, nil, companion.DefaultSubscriberOptions(), discardLogger()); err == nil {
		t.Error("expected error for nil ingester")
	}
}

func TestSubscriberWorkerLifecycle(t *testing.T) {
	t.Parallel()
	path, _ := writeCompanionPayload(t)

	getter := &fakeGetter{}
	getter.SetPointer([20]byte{0xee})
	fetcher := &fakeFetcher{path: path}
	rec := &recorderIngester{}

	opts := companion.DefaultSubscriberOptions()
	opts.Interval = time.Hour // we drive it manually via Follow / triggers
	sub, err := companion.NewSubscriber(getter, fetcher, rec, opts, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	w, err := companion.NewSubscriberWorker(sub)
	if err != nil {
		t.Fatal(err)
	}
	w.Start()
	defer w.Stop()

	// The initial run had nothing to follow, so it's a no-op.
	// Add a publisher; the worker should pick it up via the
	// trigger channel.
	var pub [32]byte
	pub[0] = 7
	w.Follow(pub, "test publisher")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if w.LastSync(pub).TorrentsImported > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	res := w.LastSync(pub)
	if res.Err != nil {
		t.Fatalf("LastSync err = %v", res.Err)
	}
	if res.TorrentsImported != 2 {
		t.Errorf("TorrentsImported = %d, want 2", res.TorrentsImported)
	}
	if got := w.Following(); len(got) != 1 {
		t.Errorf("Following() len = %d, want 1", len(got))
	}

	// Unfollow and confirm the state is dropped.
	w.Unfollow(pub)
	if got := w.Following(); len(got) != 0 {
		t.Errorf("Following() len after unfollow = %d, want 0", len(got))
	}
}

func TestSubscriberIngestReader(t *testing.T) {
	t.Parallel()
	path, _ := writeCompanionPayload(t)

	getter := &fakeGetter{}
	fetcher := &fakeFetcher{}
	rec := &recorderIngester{}

	sub, err := companion.NewSubscriber(getter, fetcher, rec, companion.DefaultSubscriberOptions(), discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	idx, tCount, cCount, err := sub.IngestReader(f)
	if err != nil {
		t.Fatalf("IngestReader: %v", err)
	}
	if len(idx.Torrents) != 2 {
		t.Errorf("idx.Torrents = %d, want 2", len(idx.Torrents))
	}
	if tCount != 2 || cCount != 3 {
		t.Errorf("tCount=%d cCount=%d, want 2,3", tCount, cCount)
	}
}
