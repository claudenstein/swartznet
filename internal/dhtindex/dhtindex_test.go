package dhtindex

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/dht/v2/traversal"

	"github.com/swartznet/swartznet/contracts/dhtschema"
	"github.com/swartznet/swartznet/contracts/token"
	"github.com/swartznet/swartznet/internal/reputation"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func genKey(t *testing.T) (ed25519.PrivateKey, [32]byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var arr [32]byte
	copy(arr[:], pub)
	return priv, arr
}

// countingPutter wraps a store, counting Put calls and optionally failing.
type countingPutter struct {
	mu      sync.Mutex
	pub     [32]byte
	calls   int
	salts   []string
	failErr error
	store   map[string]dhtschema.KeywordValue
}

func newCountingPutter(pub [32]byte) *countingPutter {
	return &countingPutter{pub: pub, store: map[string]dhtschema.KeywordValue{}}
}
func (p *countingPutter) PublicKey() [32]byte { return p.pub }
func (p *countingPutter) Put(_ context.Context, salt []byte, v dhtschema.KeywordValue) error {
	if _, err := dhtschema.EncodeValue(v); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.salts = append(p.salts, string(salt))
	if p.failErr != nil {
		return p.failErr
	}
	p.store[string(salt)] = v
	return nil
}
func (p *countingPutter) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// ---- checkPutStats (fail-closed guard) ----

func TestCheckPutStatsFailsClosed(t *testing.T) {
	t.Parallel()
	if err := checkPutStats(nil, "put"); err == nil {
		t.Error("nil stats must fail closed")
	}
	if err := checkPutStats(&traversal.Stats{NumResponses: 0}, "put"); err == nil {
		t.Error("zero-response stats must fail closed")
	} else if !strings.Contains(err.Error(), "reached zero DHT nodes") {
		t.Errorf("error = %q, want 'reached zero DHT nodes'", err.Error())
	}
	if err := checkPutStats(&traversal.Stats{NumResponses: 1}, "put"); err != nil {
		t.Errorf("one-response stats must pass: %v", err)
	}
}

// ---- Manifest ----

func TestAddHitEvictsOldestUnderCap(t *testing.T) {
	t.Parallel()
	m, _ := LoadOrCreateManifest("")
	for i := 0; i < 40; i++ {
		ih := bytes.Repeat([]byte{byte(i + 1)}, 20)
		if _, err := m.AddHit("ubuntu", dhtschema.KeywordHit{IH: ih, N: strings.Repeat("x", 20)}); err != nil {
			t.Fatalf("AddHit %d: %v", i, err)
		}
	}
	entry := m.Snapshot()["ubuntu"]
	if entry == nil {
		t.Fatal("entry missing")
	}
	if len(entry.Hits) >= 40 {
		t.Errorf("no eviction: %d hits", len(entry.Hits))
	}
	if sz := dhtschema.EstimateValueSize(dhtschema.KeywordValue{Hits: entry.Hits}); sz > dhtschema.MaxValueBytes {
		t.Errorf("entry over cap after eviction: %d bytes", sz)
	}
	// The live encode (with a timestamp) must also stay under the cap.
	if _, err := dhtschema.EncodeValue(dhtschema.KeywordValue{Hits: entry.Hits}); err != nil {
		t.Errorf("near-cap entry fails live encode: %v", err)
	}
}

func TestManifestSaveLoadRoundTrip(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/publisher.json"
	m, err := LoadOrCreateManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	ih := bytes.Repeat([]byte{7}, 20)
	m.AddHit("ubuntu", dhtschema.KeywordHit{IH: ih, N: "Ubuntu 24.04", S: 100})
	m.MarkPublished("ubuntu", time.Now())
	if err := m.Save(); err != nil {
		t.Fatal(err)
	}
	m2, err := LoadOrCreateManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	e := m2.Snapshot()["ubuntu"]
	if e == nil || len(e.Hits) != 1 || e.PublishCount != 1 {
		t.Fatalf("round-trip lost state: %+v", e)
	}
}

// ---- legacyKeyword backend ----

func newLegacyForTest(t *testing.T, minPut time.Duration) (*legacyKeyword, *countingPutter, *SharedMemoryStore) {
	t.Helper()
	priv, pub := genKey(t)
	shared := NewSharedMemoryStore()
	put := newCountingPutter(pub)
	_ = priv
	m, _ := LoadOrCreateManifest("")
	b := NewLegacyKeyword(put, shared.Getter(), m, PublisherOptions{MinPutInterval: minPut}, discardLog())
	return b, put, shared
}

func TestLegacyKeywordPublishThenThrottle(t *testing.T) {
	t.Parallel()
	b, put, _ := newLegacyForTest(t, time.Hour)
	hit := dhtschema.KeywordHit{IH: bytes.Repeat([]byte{1}, 20), N: "ubuntu"}
	if err := b.Publish(context.Background(), []string{"ubuntu"}, hit); err != nil {
		t.Fatal(err)
	}
	if put.count() != 1 {
		t.Fatalf("first publish put count = %d, want 1", put.count())
	}
	// Second publish of the same keyword within MinPutInterval must NOT put.
	if err := b.Publish(context.Background(), []string{"ubuntu"}, hit); err != nil {
		t.Fatal(err)
	}
	if put.count() != 1 {
		t.Errorf("throttle failed: put count = %d, want still 1", put.count())
	}
	// But the manifest still recorded the hit update.
	if st := b.Status(); st.TotalKeywords != 1 || st.TotalHits != 1 {
		t.Errorf("status = %+v", st)
	}
}

func TestLegacyKeywordRefreshRepublishesAll(t *testing.T) {
	t.Parallel()
	b, put, _ := newLegacyForTest(t, 0) // 0 disables the throttle
	hit := dhtschema.KeywordHit{IH: bytes.Repeat([]byte{1}, 20), N: "ubuntu desktop"}
	if err := b.Publish(context.Background(), []string{"ubuntu", "desktop"}, hit); err != nil {
		t.Fatal(err)
	}
	if put.count() != 2 {
		t.Fatalf("publish count = %d, want 2", put.count())
	}
	if err := b.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if put.count() != 4 {
		t.Errorf("after refresh count = %d, want 4 (both keywords re-announced)", put.count())
	}
}

func TestLegacyKeywordRetractScrubsInfohash(t *testing.T) {
	t.Parallel()
	b, _, _ := newLegacyForTest(t, 0)
	ih := bytes.Repeat([]byte{9}, 20)
	var ihArr [20]byte
	copy(ihArr[:], ih)
	if err := b.Publish(context.Background(), []string{"ubuntu", "desktop"}, dhtschema.KeywordHit{IH: ih, N: "ubuntu"}); err != nil {
		t.Fatal(err)
	}
	if b.Status().TotalKeywords != 2 {
		t.Fatal("expected 2 keywords before retract")
	}
	if err := b.Retract(context.Background(), ihArr); err != nil {
		t.Fatal(err)
	}
	if st := b.Status(); st.TotalKeywords != 0 || st.TotalHits != 0 {
		t.Errorf("retract did not scrub: %+v", st)
	}
}

func TestLegacyKeywordLookupResolvesPublishedHit(t *testing.T) {
	t.Parallel()
	// Publisher and searcher share one store; the publisher puts, the searcher
	// resolves via backend.Lookup(pub, token).
	priv, pub := genKey(t)
	shared := NewSharedMemoryStore()
	m, _ := LoadOrCreateManifest("")
	pubBackend := NewLegacyKeyword(shared.PutterFor(priv), shared.Getter(), m, PublisherOptions{}, discardLog())
	ih := bytes.Repeat([]byte{0xab}, 20)
	if err := pubBackend.Publish(context.Background(), []string{"ubuntu"}, dhtschema.KeywordHit{IH: ih, N: "Ubuntu 24.04", S: 42}); err != nil {
		t.Fatal(err)
	}
	// Read side uses a getter-only backend over the same store.
	readBackend := NewLegacyKeyword(nil, shared.Getter(), mustMem(), PublisherOptions{}, discardLog())
	hits, err := readBackend.Lookup(context.Background(), pub, "ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].N != "Ubuntu 24.04" || hits[0].S != 42 {
		t.Errorf("lookup hits = %+v", hits)
	}
}

func mustMem() *Manifest { m, _ := LoadOrCreateManifest(""); return m }

// ---- Publisher worker ----

func TestPublisherTokenizesNameOnly(t *testing.T) {
	t.Parallel()
	_, pub := genKey(t)
	put := newCountingPutter(pub)
	m, _ := LoadOrCreateManifest("")
	backend := NewLegacyKeyword(put, NewSharedMemoryStore().Getter(), m, PublisherOptions{}, discardLog())
	p := NewPublisher(backend, PublisherOptions{RefreshInterval: time.Hour}, discardLog())
	p.Start()
	defer p.Stop()

	name := "Debian Bookworm netinst amd64"
	p.Submit(PublishTask{InfoHash: bytes.Repeat([]byte{3}, 20), Name: name, Seeders: 5})

	want := token.Tokenize(name) // name-derived keywords only
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(m.Keywords()) == len(want) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	got := m.Keywords()
	if len(got) != len(want) {
		t.Fatalf("published keywords = %v, want the name tokens %v", got, want)
	}
	wantSet := map[string]bool{}
	for _, w := range want {
		wantSet[w] = true
	}
	for _, g := range got {
		if !wantSet[g] {
			t.Errorf("published keyword %q is not a name token — content leaked to Layer D", g)
		}
	}
}

func TestPublisherBadInfohashIgnored(t *testing.T) {
	t.Parallel()
	_, pub := genKey(t)
	put := newCountingPutter(pub)
	m, _ := LoadOrCreateManifest("")
	backend := NewLegacyKeyword(put, NewSharedMemoryStore().Getter(), m, PublisherOptions{}, discardLog())
	p := NewPublisher(backend, PublisherOptions{RefreshInterval: time.Hour}, discardLog())
	p.Start()
	defer p.Stop()
	p.Submit(PublishTask{InfoHash: []byte{1, 2, 3}, Name: "ubuntu"}) // len != 20
	time.Sleep(200 * time.Millisecond)
	if len(m.Keywords()) != 0 {
		t.Errorf("bad infohash was published: %v", m.Keywords())
	}
}

// ---- Lookup ----

// stubBackend implements RecordBackend with a settable per-indexer hit map.
type stubBackend struct {
	hits map[[32]byte][]dhtschema.KeywordHit
}

func (s *stubBackend) Publish(context.Context, []string, dhtschema.KeywordHit) error { return nil }
func (s *stubBackend) Refresh(context.Context) error                                 { return nil }
func (s *stubBackend) Retract(context.Context, [20]byte) error                       { return nil }
func (s *stubBackend) Lookup(_ context.Context, pub [32]byte, _ string) ([]dhtschema.KeywordHit, error) {
	return s.hits[pub], nil
}
func (s *stubBackend) Status() PublisherStatus { return PublisherStatus{} }
func (s *stubBackend) Close() error            { return nil }

func TestLookupPicksMostDistinctiveToken(t *testing.T) {
	t.Parallel()
	// Equivalence pin: MostDistinctive over the tokenizer output is the sole
	// chooser — "new ubuntu" derives "ubuntu", never the first token "new".
	if got := token.MostDistinctive(token.Tokenize("new ubuntu")); got != "ubuntu" {
		t.Fatalf("MostDistinctive(\"new ubuntu\") = %q, want ubuntu", got)
	}
	var indexerPub [32]byte
	indexerPub[0] = 0xaa
	ih := bytes.Repeat([]byte{1}, 20)
	// The stub only answers for the "ubuntu" namespace (keyed on pubkey, not
	// token) — the test asserts a non-empty result, proving the query reached
	// the indexer via the distinctive token rather than erroring on "new".
	backend := &stubBackend{hits: map[[32]byte][]dhtschema.KeywordHit{
		indexerPub: {{IH: ih, N: "Ubuntu 24.04", S: 7}},
	}}
	lk := NewLookup(backend)
	lk.AddIndexer(indexerPub, "idx")
	resp, err := lk.Query(context.Background(), "new ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	if resp.IndexersAsked != 1 || resp.IndexersResponded != 1 || len(resp.Hits) != 1 {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.Hits[0].InfoHash != hex.EncodeToString(ih) {
		t.Errorf("hit infohash = %s", resp.Hits[0].InfoHash)
	}
}

func TestLookupEmptyIndexerSet(t *testing.T) {
	t.Parallel()
	lk := NewLookup(&stubBackend{})
	resp, err := lk.Query(context.Background(), "ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	if resp.IndexersAsked != 0 || len(resp.Hits) != 0 {
		t.Errorf("empty set must yield empty response, got %+v", resp)
	}
}

func TestLookupNoTokensErrors(t *testing.T) {
	t.Parallel()
	lk := NewLookup(&stubBackend{})
	if _, err := lk.Query(context.Background(), "  !!  "); err == nil {
		t.Error("query with no tokens must error")
	}
}

func TestLookupMergesAcrossIndexers(t *testing.T) {
	t.Parallel()
	var a, b [32]byte
	a[0], b[0] = 1, 2
	ih := bytes.Repeat([]byte{5}, 20)
	backend := &stubBackend{hits: map[[32]byte][]dhtschema.KeywordHit{
		a: {{IH: ih, N: "ubuntu", S: 10}},
		b: {{IH: ih, N: "ubuntu", S: 50}}, // higher seeders wins
	}}
	lk := NewLookup(backend)
	lk.AddIndexer(a, "a")
	lk.AddIndexer(b, "b")
	resp, err := lk.Query(context.Background(), "ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Hits) != 1 {
		t.Fatalf("merge failed: %d hits", len(resp.Hits))
	}
	h := resp.Hits[0]
	if h.Seeders != 50 {
		t.Errorf("seeders = %d, want 50 (max across sources)", h.Seeders)
	}
	if len(h.Sources) != 2 {
		t.Errorf("sources = %d, want 2", len(h.Sources))
	}
}

func TestLookupReputationGateFiltersLowScore(t *testing.T) {
	t.Parallel()
	var good, bad [32]byte
	good[0], bad[0] = 0x11, 0x22
	ih := bytes.Repeat([]byte{6}, 20)
	backend := &stubBackend{hits: map[[32]byte][]dhtschema.KeywordHit{
		good: {{IH: ih, N: "ubuntu"}},
		bad:  {{IH: bytes.Repeat([]byte{7}, 20), N: "malware"}},
	}}
	tracker := reputation.NewTracker()
	// Model the bad indexer as one that returned hits which all got flagged,
	// dragging its smoothed score well below 0.4; the good indexer keeps the
	// neutral 0.5 prior (never touched).
	tracker.RecordReturned(reputation.PubKey(bad), 10)
	for i := 0; i < 10; i++ {
		tracker.RecordFlagged(reputation.PubKey(bad))
	}
	lk := NewLookup(backend)
	lk.SetTracker(tracker)
	lk.SetMinIndexerScore(0.4)
	lk.AddIndexer(good, "good")
	lk.AddIndexer(bad, "bad")
	resp, err := lk.Query(context.Background(), "ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	if resp.IndexersAsked != 1 {
		t.Errorf("reputation gate did not filter: asked=%d, want 1", resp.IndexersAsked)
	}
}

// TestLookupDedupsDuplicateHitsPerIndexer pins the anti-spam fix: a single
// indexer that repeats one infohash in its own KeywordValue cannot inflate the
// source count or the multi-source score bonus. A hostile single indexer
// repeating X five times must NOT outrank a genuine 3-distinct-indexer hit Y.
func TestLookupDedupsDuplicateHitsPerIndexer(t *testing.T) {
	t.Parallel()
	var hostile, g1, g2, g3 [32]byte
	hostile[0], g1[0], g2[0], g3[0] = 0x01, 0x02, 0x03, 0x04
	ihX := bytes.Repeat([]byte{0xaa}, 20)
	ihY := bytes.Repeat([]byte{0xbb}, 20)
	xHex, yHex := hex.EncodeToString(ihX), hex.EncodeToString(ihY)
	backend := &stubBackend{hits: map[[32]byte][]dhtschema.KeywordHit{
		// Hostile indexer lists X five times in one response.
		hostile: {
			{IH: ihX, N: "x"}, {IH: ihX, N: "x"}, {IH: ihX, N: "x"},
			{IH: ihX, N: "x"}, {IH: ihX, N: "x"},
		},
		// Three DISTINCT indexers each return Y once (genuine consensus).
		g1: {{IH: ihY, N: "y"}},
		g2: {{IH: ihY, N: "y"}},
		g3: {{IH: ihY, N: "y"}},
	}}
	lk := NewLookup(backend)
	lk.AddIndexer(hostile, "hostile")
	lk.AddIndexer(g1, "g1")
	lk.AddIndexer(g2, "g2")
	lk.AddIndexer(g3, "g3")
	resp, err := lk.Query(context.Background(), "ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	var hitX, hitY *LookupHit
	for i := range resp.Hits {
		switch resp.Hits[i].InfoHash {
		case xHex:
			hitX = &resp.Hits[i]
		case yHex:
			hitY = &resp.Hits[i]
		}
	}
	if hitX == nil || hitY == nil {
		t.Fatalf("missing hits: X=%v Y=%v", hitX, hitY)
	}
	if len(hitX.Sources) != 1 {
		t.Errorf("hostile duplicate hits inflated source count: X sources = %d, want 1", len(hitX.Sources))
	}
	if len(hitY.Sources) != 3 {
		t.Errorf("genuine consensus source count = %d, want 3", len(hitY.Sources))
	}
	// The 3-indexer consensus hit must score at least as high as the faked one.
	if hitX.Score > hitY.Score {
		t.Errorf("spoofed single-indexer hit (%.3f) outranks genuine consensus (%.3f)", hitX.Score, hitY.Score)
	}
}

func TestLookupNotePublisherSeenAddsIndexer(t *testing.T) {
	t.Parallel()
	lk := NewLookup(&stubBackend{})
	var pub [32]byte
	pub[0] = 0x33
	lk.NotePublisherSeen(pub)
	if len(lk.Indexers()) != 1 {
		t.Errorf("NotePublisherSeen did not add the indexer")
	}
}

// ---- BEP-46 pointer decode cap ----

func TestDecodePointerValueRejectsOversize(t *testing.T) {
	t.Parallel()
	big := make([]byte, dhtschema.MaxValueBytes+1)
	if _, err := decodePointerValue(big); err == nil {
		t.Error("oversize pointer value must be rejected before unmarshal")
	}
}

func TestDecodePointerValueBadLength(t *testing.T) {
	t.Parallel()
	// A well-formed bencode pointer with a 10-byte ih must be rejected.
	raw := []byte("d2:ih10:0123456789e")
	if _, err := decodePointerValue(raw); err == nil {
		t.Error("pointer with non-20-byte ih must be rejected")
	}
}

// ---- SampleInfohashes nil guards ----

func TestSampleInfohashesNilGuards(t *testing.T) {
	t.Parallel()
	if _, err := SampleInfohashes(context.Background(), nil, nil, [20]byte{}); err == nil {
		t.Error("nil server must error")
	}
}
