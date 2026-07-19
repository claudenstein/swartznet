package companion

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/swartznet/swartznet/internal/indexer"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// ---- fakes ----

type fakeCorpus struct {
	torrents []indexer.TorrentDoc
	content  map[string][]indexer.ContentDoc
	err      error
}

func (f *fakeCorpus) AllTorrentDocs() ([]indexer.TorrentDoc, error) { return f.torrents, f.err }
func (f *fakeCorpus) ContentDocsForInfoHash(ih string) ([]indexer.ContentDoc, error) {
	return f.content[ih], nil
}

// sharedPointerStore is an in-memory BEP-46 pointer map shared by a putter and
// a getter, keyed by pubkey.
type sharedPointerStore struct {
	mu    sync.Mutex
	store map[[32]byte][20]byte
}

func newPointerStore() *sharedPointerStore {
	return &sharedPointerStore{store: make(map[[32]byte][20]byte)}
}

type memPutter struct {
	store *sharedPointerStore
	pub   [32]byte
}

func (p *memPutter) PutInfohashPointer(_ context.Context, _ []byte, ih [20]byte) error {
	p.store.mu.Lock()
	p.store.store[p.pub] = ih
	p.store.mu.Unlock()
	return nil
}

type memGetter struct{ store *sharedPointerStore }

func (g *memGetter) GetInfohashPointer(_ context.Context, pub [32]byte, _ []byte) ([20]byte, error) {
	g.store.mu.Lock()
	defer g.store.mu.Unlock()
	ih, ok := g.store.store[pub]
	if !ok {
		return [20]byte{}, errors.New("pointer not found")
	}
	return ih, nil
}

type memSeeder struct {
	mu      sync.Mutex
	seen    []*metainfo.MetaInfo
	dropped [][20]byte
}

func (s *memSeeder) SeedMetaInfo(mi *metainfo.MetaInfo, _ string) error {
	s.mu.Lock()
	s.seen = append(s.seen, mi)
	s.mu.Unlock()
	return nil
}
func (s *memSeeder) DropTorrent(ih [20]byte) error {
	s.mu.Lock()
	s.dropped = append(s.dropped, ih)
	s.mu.Unlock()
	return nil
}

// pathFetcher returns a fixed on-disk path regardless of infohash (stands in
// for the fail-closed engine fetch — the bounds are unit-tested separately).
type pathFetcher struct {
	path string
	err  error
}

func (f *pathFetcher) FetchCompanionTorrent(_ context.Context, _ [20]byte) (string, error) {
	return f.path, f.err
}

type recorder struct {
	mu       sync.Mutex
	torrents []indexer.TorrentDoc
	content  []indexer.ContentDoc
	failOn   string // infohash to fail IndexTorrent on
}

func (r *recorder) IndexTorrent(d indexer.TorrentDoc) error {
	if r.failOn != "" && d.InfoHash == r.failOn {
		return errors.New("boom")
	}
	r.mu.Lock()
	r.torrents = append(r.torrents, d)
	r.mu.Unlock()
	return nil
}
func (r *recorder) IndexContent(d indexer.ContentDoc) error {
	r.mu.Lock()
	r.content = append(r.content, d)
	r.mu.Unlock()
	return nil
}

func keypair(seed byte) [32]byte {
	var pub [32]byte
	for i := range pub {
		pub[i] = seed + byte(i)
	}
	return pub
}

// ---- serialize ----

func TestEncodeDecodeRoundTrip(t *testing.T) {
	t.Parallel()
	idx := CompanionIndex{
		Publisher:   strings.Repeat("ab", 32),
		GeneratedAt: 1700000000,
		Torrents: []TorrentRecord{{
			InfoHash: strings.Repeat("1", 40), Name: "ubuntu", Size: 100,
			Files: []FileRecord{{Index: 0, Path: "a.txt", Mime: "text/plain", Chunks: []ContentChunk{{Text: "hello"}}}},
		}},
	}
	raw, err := Encode(idx)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != FormatVersion || got.Format != FormatName {
		t.Errorf("version/format = %d/%q", got.Version, got.Format)
	}
	if len(got.Torrents) != 1 || got.Torrents[0].Files[0].Chunks[0].Text != "hello" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestEncodeNilTorrentsIsEmptySlice(t *testing.T) {
	t.Parallel()
	raw, _ := Encode(CompanionIndex{GeneratedAt: 1})
	got, err := Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got.Torrents == nil {
		t.Error("Torrents decoded as nil; want empty slice")
	}
}

func TestDecodeRefusesBadFormatAndVersion(t *testing.T) {
	t.Parallel()
	// Hand-craft a gzip(JSON) with a wrong format.
	bad := mustGzipJSON(t, `{"version":1,"format":"evil","generated_at":1,"torrents":[]}`)
	if _, err := Decode(bytes.NewReader(bad)); err == nil || !strings.Contains(err.Error(), "bad format") {
		t.Errorf("bad format not refused: %v", err)
	}
	badv := mustGzipJSON(t, `{"version":999,"format":"swartznet-content-index","generated_at":1,"torrents":[]}`)
	if _, err := Decode(bytes.NewReader(badv)); err == nil || !strings.Contains(err.Error(), "unsupported version") {
		t.Errorf("bad version not refused: %v", err)
	}
}

func TestCompanionFileName(t *testing.T) {
	t.Parallel()
	if got := CompanionFileName(""); got != FormatFileName {
		t.Errorf("empty = %q", got)
	}
	if got := CompanionFileName("abcdef0123456789"); got != "swartznet-content-index-abcdef012345-v1.json.gz" {
		t.Errorf("named = %q", got)
	}
}

// ---- build ----

func TestBuildFromIndex(t *testing.T) {
	t.Parallel()
	ih := strings.Repeat("2", 40)
	src := &fakeCorpus{
		torrents: []indexer.TorrentDoc{{InfoHash: strings.ToUpper(ih), Name: "Ubuntu", FilePaths: []string{"a.txt", "b.txt"}, SizeBytes: 500}},
		content: map[string][]indexer.ContentDoc{
			strings.ToUpper(ih): {{InfoHash: ih, FileIndex: 0, FilePath: "a.txt", Mime: "text/plain", Extractor: "plaintext", Text: "chunk one"}},
		},
	}
	out, err := BuildFromIndex(src, "cafe", DefaultBuildOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Torrents) != 1 {
		t.Fatalf("torrents = %d", len(out.Torrents))
	}
	tr := out.Torrents[0]
	if tr.InfoHash != ih {
		t.Errorf("infohash not lowercased: %q", tr.InfoHash)
	}
	if len(tr.Files) != 2 { // one record per file path (b.txt has no content)
		t.Fatalf("files = %d, want 2", len(tr.Files))
	}
	if len(tr.Files[0].Chunks) != 1 || tr.Files[0].Chunks[0].Text != "chunk one" {
		t.Errorf("file 0 chunks = %+v", tr.Files[0].Chunks)
	}
	if len(tr.Files[1].Chunks) != 0 {
		t.Errorf("file 1 (no content) should have no chunks")
	}
	if out.Publisher != "cafe" {
		t.Errorf("publisher = %q", out.Publisher)
	}
}

// ---- publisher ----

func newTestPublisher(t *testing.T, src CorpusSource, pub [32]byte) (*Publisher, *sharedPointerStore, *memSeeder) {
	t.Helper()
	store := newPointerStore()
	seeder := &memSeeder{}
	opts := DefaultPublisherOptions()
	opts.Dir = t.TempDir()
	opts.PublisherKey = pub
	opts.MinInterval = 0
	p, err := NewPublisher(src, &memPutter{store: store, pub: pub}, seeder, opts, discardLog())
	if err != nil {
		t.Fatal(err)
	}
	return p, store, seeder
}

func TestPublisherEmptyIndexIsFailure(t *testing.T) {
	t.Parallel()
	pub := keypair(1)
	p, store, _ := newTestPublisher(t, &fakeCorpus{}, pub)
	p.refreshOnce(context.Background())
	st := p.Status()
	if !st.LastRefresh.IsZero() {
		t.Error("lastRefresh advanced on an empty index")
	}
	if !strings.Contains(st.LastError, "empty local index") {
		t.Errorf("lastError = %q", st.LastError)
	}
	store.mu.Lock()
	_, published := store.store[pub]
	store.mu.Unlock()
	if published {
		t.Error("empty index still published a pointer")
	}
}

func TestPublisherSuccessAdvancesLastRefresh(t *testing.T) {
	t.Parallel()
	pub := keypair(2)
	src := &fakeCorpus{torrents: []indexer.TorrentDoc{{InfoHash: strings.Repeat("3", 40), Name: "ubuntu", FilePaths: []string{"a"}}}}
	p, store, seeder := newTestPublisher(t, src, pub)
	p.refreshOnce(context.Background())
	st := p.Status()
	if st.LastRefresh.IsZero() || st.PublishedCount != 1 || st.LastError != "" {
		t.Fatalf("status after publish = %+v", st)
	}
	store.mu.Lock()
	_, published := store.store[pub]
	store.mu.Unlock()
	if !published {
		t.Error("no pointer published")
	}
	if len(seeder.seen) != 1 {
		t.Errorf("seeder saw %d metainfos, want 1", len(seeder.seen))
	}
}

// TestPublisherDropsPreviousSeed pins the leak fix: republishing (a new
// GeneratedAt → new infohash) drops the previously-seeded companion torrent so
// they do not accumulate.
func TestPublisherDropsPreviousSeed(t *testing.T) {
	t.Parallel()
	pub := keypair(7)
	src := &fakeCorpus{torrents: []indexer.TorrentDoc{{InfoHash: strings.Repeat("8", 40), Name: "x", FilePaths: []string{"a"}}}}
	p, _, seeder := newTestPublisher(t, src, pub)
	p.refreshOnce(context.Background()) // seed #1
	// Force a distinct payload so the next build yields a new infohash.
	src.torrents[0].Name = "y"
	p.refreshOnce(context.Background()) // seed #2 → must drop #1
	seeder.mu.Lock()
	defer seeder.mu.Unlock()
	if len(seeder.seen) != 2 {
		t.Fatalf("seeded %d, want 2", len(seeder.seen))
	}
	if len(seeder.dropped) != 1 {
		t.Errorf("dropped %d previous seeds, want 1 (accumulation leak)", len(seeder.dropped))
	}
}

func TestPublisherRefreshNowThrottle(t *testing.T) {
	t.Parallel()
	pub := keypair(3)
	src := &fakeCorpus{torrents: []indexer.TorrentDoc{{InfoHash: strings.Repeat("4", 40), Name: "x", FilePaths: []string{"a"}}}}
	p, _, _ := newTestPublisher(t, src, pub)
	p.opts.MinInterval = time.Hour
	p.refreshOnce(context.Background()) // sets lastAttempt
	if err := p.RefreshNow(); err != ErrTooSoon {
		t.Errorf("RefreshNow = %v, want ErrTooSoon", err)
	}
}

// ---- subscriber (the security-critical path) ----

// publishToDisk runs a publisher's build+write and returns (dir path to the
// .json.gz, pubkey hex, the infohash pointed at).
func publishToDisk(t *testing.T, pub [32]byte, src CorpusSource) (string, string) {
	t.Helper()
	pubHex := hex.EncodeToString(pub[:])
	idx, err := BuildFromIndex(src, pubHex, DefaultBuildOptions())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	jsonPath, _, err := WriteCompanionFiles(dir, idx)
	if err != nil {
		t.Fatal(err)
	}
	return jsonPath, pubHex
}

func TestSubscriberImportsAndStampsSignedBy(t *testing.T) {
	t.Parallel()
	pub := keypair(10)
	ih := strings.Repeat("5", 40)
	src := &fakeCorpus{
		torrents: []indexer.TorrentDoc{{InfoHash: ih, Name: "ubuntu", FilePaths: []string{"a.txt"}}},
		content:  map[string][]indexer.ContentDoc{ih: {{InfoHash: ih, FileIndex: 0, FilePath: "a.txt", Text: "body"}}},
	}
	jsonPath, pubHex := publishToDisk(t, pub, src)
	rec := &recorder{}
	sub, err := NewSubscriber(&memGetter{store: newPointerStore()}, &pathFetcher{path: jsonPath}, rec, DefaultSubscriberOptions(), discardLog())
	if err != nil {
		t.Fatal(err)
	}
	// The pointer getter is unused here (we bypass it by calling Sync, which
	// still calls the getter) — so give it the pointer.
	sub.getter = &memGetter{store: storeWith(pub, [20]byte{1})}
	res := sub.Sync(context.Background(), pub)
	if res.Err != nil {
		t.Fatalf("sync err: %v", res.Err)
	}
	if res.TorrentsImported != 1 || res.ContentImported != 1 {
		t.Fatalf("imported t=%d c=%d", res.TorrentsImported, res.ContentImported)
	}
	if len(rec.torrents) != 1 || rec.torrents[0].SignedBy != pubHex {
		t.Errorf("SignedBy not stamped: %+v", rec.torrents)
	}
}

func TestSubscriberRejectsPublisherMismatch(t *testing.T) {
	t.Parallel()
	author := keypair(11)
	follower := keypair(99) // we follow a DIFFERENT key than the snapshot's author
	ih := strings.Repeat("6", 40)
	src := &fakeCorpus{torrents: []indexer.TorrentDoc{{InfoHash: ih, Name: "x", FilePaths: []string{"a"}}}}
	jsonPath, _ := publishToDisk(t, author, src)
	rec := &recorder{}
	sub, _ := NewSubscriber(&memGetter{store: storeWith(follower, [20]byte{2})}, &pathFetcher{path: jsonPath}, rec, DefaultSubscriberOptions(), discardLog())
	res := sub.Sync(context.Background(), follower)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "publisher mismatch") {
		t.Fatalf("mismatch not rejected: %v", res.Err)
	}
	if len(rec.torrents) != 0 {
		t.Error("records imported despite publisher mismatch")
	}
}

func TestSubscriberDedupsUnchangedSnapshot(t *testing.T) {
	t.Parallel()
	pub := keypair(12)
	ih := strings.Repeat("7", 40)
	src := &fakeCorpus{torrents: []indexer.TorrentDoc{{InfoHash: ih, Name: "x", FilePaths: []string{"a"}}}}
	jsonPath, _ := publishToDisk(t, pub, src)
	rec := &recorder{}
	sub, _ := NewSubscriber(&memGetter{store: storeWith(pub, [20]byte{3})}, &pathFetcher{path: jsonPath}, rec, DefaultSubscriberOptions(), discardLog())
	first := sub.Sync(context.Background(), pub)
	if first.Err != nil || first.Deduped {
		t.Fatalf("first sync: %+v", first)
	}
	second := sub.Sync(context.Background(), pub)
	if !second.Deduped {
		t.Error("unchanged snapshot was not deduped")
	}
	if len(rec.torrents) != 1 {
		t.Errorf("re-ingested on dedup: %d torrents", len(rec.torrents))
	}
}

func TestSubscriberPointerFailurePopulatesResult(t *testing.T) {
	t.Parallel()
	pub := keypair(13)
	rec := &recorder{}
	sub, _ := NewSubscriber(&memGetter{store: newPointerStore()}, &pathFetcher{}, rec, DefaultSubscriberOptions(), discardLog())
	res := sub.Sync(context.Background(), pub)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "get pointer") {
		t.Errorf("pointer failure not surfaced: %v", res.Err)
	}
	if res.Publisher != hex.EncodeToString(pub[:]) {
		t.Error("result missing publisher on failure")
	}
}

// ---- worker ----

func TestSubscriberWorkerFollowUnfollow(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	sub, _ := NewSubscriber(&memGetter{store: newPointerStore()}, &pathFetcher{err: errors.New("no")}, rec, DefaultSubscriberOptions(), discardLog())
	w, err := NewSubscriberWorker(sub)
	if err != nil {
		t.Fatal(err)
	}
	pub := keypair(20)
	w.Follow(pub, "seed-1")
	if got := w.Following(); len(got) != 1 || got[pub] != "seed-1" {
		t.Errorf("follow set = %v", got)
	}
	w.Unfollow(pub)
	if len(w.Following()) != 0 {
		t.Error("unfollow did not remove")
	}
}

// ---- helpers ----

func storeWith(pub [32]byte, ih [20]byte) *sharedPointerStore {
	s := newPointerStore()
	s.store[pub] = ih
	return s
}

func mustGzipJSON(t *testing.T, js string) []byte {
	t.Helper()
	// Encode via a CompanionIndex-free path: gzip the raw JSON.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write([]byte(js)); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
