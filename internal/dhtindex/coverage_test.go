package dhtindex_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"sort"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/dhtindex"
	"github.com/swartznet/swartznet/internal/reputation"
)

// ---------------------------------------------------------------------------
// Publisher.Status() tests
// ---------------------------------------------------------------------------

// TestPublisherStatusEmptyManifest verifies that Status returns zero
// counts when the manifest is empty (no tasks submitted).
func TestPublisherStatusEmptyManifest(t *testing.T) {
	t.Parallel()
	mf, err := dhtindex.LoadOrCreateManifest("")
	if err != nil {
		t.Fatal(err)
	}
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	mem := dhtindex.NewMemoryPutterGetter(priv)
	p := dhtindex.NewPublisher(mem, mf, dhtindex.PublisherOptions{
		PutTimeout: 1 * time.Second,
		QueueSize:  4,
	}, silentLogger())
	p.Start()
	defer p.Stop()

	status := p.Status()
	if status.TotalKeywords != 0 {
		t.Errorf("TotalKeywords = %d, want 0", status.TotalKeywords)
	}
	if status.TotalHits != 0 {
		t.Errorf("TotalHits = %d, want 0", status.TotalHits)
	}
	if len(status.LastPublishes) != 0 {
		t.Errorf("LastPublishes len = %d, want 0", len(status.LastPublishes))
	}
}

// TestPublisherStatusReflectsManifest verifies that Status returns
// correct keyword and hit counts after a task is processed. The test
// adds a task whose name yields exactly one keyword ("ubuntu") and
// checks that Status reports TotalKeywords=1, TotalHits=1, and the
// per-keyword entry has a correct PublishCount.
func TestPublisherStatusReflectsManifest(t *testing.T) {
	t.Parallel()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	mem := dhtindex.NewMemoryPutterGetter(priv)
	rec := &recordingPutter{inner: mem}

	mf, err := dhtindex.LoadOrCreateManifest("")
	if err != nil {
		t.Fatal(err)
	}
	p := dhtindex.NewPublisher(rec, mf, dhtindex.PublisherOptions{
		PutTimeout: 2 * time.Second,
		QueueSize:  16,
	}, silentLogger())
	p.Start()
	defer p.Stop()

	// "ubuntu" produces exactly one keyword token.
	p.Submit(dhtindex.PublishTask{
		InfoHash:  bytes.Repeat([]byte{0xde}, 20),
		Name:      "ubuntu",
		Seeders:   42,
		FileCount: 1,
		SizeBytes: 3 << 30,
	})

	// Wait for the worker to process the task.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(rec.snapshot()) >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	status := p.Status()
	if status.TotalKeywords != 1 {
		t.Errorf("TotalKeywords = %d, want 1", status.TotalKeywords)
	}
	if status.TotalHits != 1 {
		t.Errorf("TotalHits = %d, want 1", status.TotalHits)
	}
	if len(status.LastPublishes) != 1 {
		t.Fatalf("LastPublishes len = %d, want 1", len(status.LastPublishes))
	}
	kw := status.LastPublishes[0]
	if kw.Keyword != "ubuntu" {
		t.Errorf("Keyword = %q, want 'ubuntu'", kw.Keyword)
	}
	if kw.HitsCount != 1 {
		t.Errorf("HitsCount = %d, want 1", kw.HitsCount)
	}
	if kw.PublishCount != 1 {
		t.Errorf("PublishCount = %d, want 1", kw.PublishCount)
	}
	if kw.LastError != "" {
		t.Errorf("LastError = %q, want empty", kw.LastError)
	}
}

// TestPublisherStatusMultipleKeywords verifies that a multi-word
// torrent name produces multiple keywords, each with its own entry
// in the status output.
func TestPublisherStatusMultipleKeywords(t *testing.T) {
	t.Parallel()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	mem := dhtindex.NewMemoryPutterGetter(priv)
	rec := &recordingPutter{inner: mem}

	mf, _ := dhtindex.LoadOrCreateManifest("")
	p := dhtindex.NewPublisher(rec, mf, dhtindex.PublisherOptions{
		PutTimeout: 2 * time.Second,
		QueueSize:  16,
	}, silentLogger())
	p.Start()
	defer p.Stop()

	// "linux kernel" produces two keywords: "linux", "kernel"
	p.Submit(dhtindex.PublishTask{
		InfoHash: bytes.Repeat([]byte{0xfe}, 20),
		Name:     "linux kernel",
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(rec.snapshot()) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	status := p.Status()
	if status.TotalKeywords < 2 {
		t.Errorf("TotalKeywords = %d, want >= 2", status.TotalKeywords)
	}
	// Each keyword should have exactly one hit (the one torrent).
	if status.TotalHits < 2 {
		t.Errorf("TotalHits = %d, want >= 2 (one per keyword)", status.TotalHits)
	}
	// Check that both keywords appear.
	kwNames := make(map[string]bool)
	for _, ks := range status.LastPublishes {
		kwNames[ks.Keyword] = true
	}
	for _, want := range []string{"linux", "kernel"} {
		if !kwNames[want] {
			t.Errorf("keyword %q not found in status output; got %v", want, kwNames)
		}
	}
}

// ---------------------------------------------------------------------------
// Lookup.Query with SetTracker + SetMinIndexerScore (reputation filtering)
// ---------------------------------------------------------------------------

// TestLookupSetMinIndexerScoreNoTracker verifies that setting a min
// score without a tracker has no effect (all indexers are queried).
func TestLookupSetMinIndexerScoreNoTracker(t *testing.T) {
	t.Parallel()
	g := newScriptedGetter()
	pub := newPubkey(t)
	salt, _ := dhtindex.SaltForKeyword("fedora")
	g.set(pub, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: bytes.Repeat([]byte{0x11}, 20), N: "Fedora"},
	}})

	l := dhtindex.NewLookup(g)
	l.AddIndexer(pub, "fed-indexer")
	l.SetMinIndexerScore(0.9) // high threshold, but no tracker

	resp, err := l.Query(context.Background(), "fedora")
	if err != nil {
		t.Fatal(err)
	}
	if resp.IndexersAsked != 1 {
		t.Errorf("IndexersAsked = %d, want 1 (no tracker means no filtering)", resp.IndexersAsked)
	}
	if len(resp.Hits) != 1 {
		t.Errorf("Hits = %d, want 1", len(resp.Hits))
	}
}

// TestLookupAllIndexersFilteredReturnsEmpty verifies that when
// reputation filtering removes every known indexer, the result is a
// valid but empty response rather than an error.
func TestLookupAllIndexersFilteredReturnsEmpty(t *testing.T) {
	t.Parallel()
	g := newScriptedGetter()
	badA := newPubkey(t)
	badB := newPubkey(t)
	salt, _ := dhtindex.SaltForKeyword("arch")

	g.set(badA, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: bytes.Repeat([]byte{0x77}, 20), N: "Spam A"},
	}})
	g.set(badB, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: bytes.Repeat([]byte{0x78}, 20), N: "Spam B"},
	}})

	tracker := reputation.NewTracker()
	// Flag both indexers so their scores drop.
	for i := 0; i < 100; i++ {
		tracker.RecordReturned(reputation.PubKey(badA), 1)
		tracker.RecordFlagged(reputation.PubKey(badA))
		tracker.RecordReturned(reputation.PubKey(badB), 1)
		tracker.RecordFlagged(reputation.PubKey(badB))
	}

	l := dhtindex.NewLookup(g)
	l.AddIndexer(badA, "bad-a")
	l.AddIndexer(badB, "bad-b")
	l.SetTracker(tracker)
	l.SetMinIndexerScore(0.5)

	resp, err := l.Query(context.Background(), "arch")
	if err != nil {
		t.Fatal(err)
	}
	if resp.IndexersAsked != 0 {
		t.Errorf("IndexersAsked = %d, want 0 (all filtered)", resp.IndexersAsked)
	}
	if len(resp.Hits) != 0 {
		t.Errorf("Hits = %d, want 0", len(resp.Hits))
	}
}

// TestLookupMinScoreZeroDisablesFiltering verifies that setting
// MinIndexerScore to 0 (the default) means even low-reputation
// indexers are queried.
func TestLookupMinScoreZeroDisablesFiltering(t *testing.T) {
	t.Parallel()
	g := newScriptedGetter()
	bad := newPubkey(t)
	salt, _ := dhtindex.SaltForKeyword("gentoo")

	g.set(bad, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: bytes.Repeat([]byte{0x99}, 20), N: "Gentoo"},
	}})

	tracker := reputation.NewTracker()
	for i := 0; i < 100; i++ {
		tracker.RecordReturned(reputation.PubKey(bad), 1)
		tracker.RecordFlagged(reputation.PubKey(bad))
	}

	l := dhtindex.NewLookup(g)
	l.AddIndexer(bad, "bad")
	l.SetTracker(tracker)
	// minScore defaults to 0 — don't set it

	resp, err := l.Query(context.Background(), "gentoo")
	if err != nil {
		t.Fatal(err)
	}
	if resp.IndexersAsked != 1 {
		t.Errorf("IndexersAsked = %d, want 1 (minScore=0 means no filtering)", resp.IndexersAsked)
	}
	if len(resp.Hits) != 1 {
		t.Errorf("Hits = %d, want 1", len(resp.Hits))
	}
}

// ---------------------------------------------------------------------------
// Lookup.Query with SetBloom (bloom marking)
// ---------------------------------------------------------------------------

// TestLookupBloomMarkingMultipleHits verifies that when multiple
// hits are returned, only those whose infohash is in the bloom
// filter get BloomHit=true.
func TestLookupBloomMarkingMultipleHits(t *testing.T) {
	t.Parallel()
	g := newScriptedGetter()
	pub := newPubkey(t)
	salt, _ := dhtindex.SaltForKeyword("debian")

	knownIH := bytes.Repeat([]byte{0x11}, 20)
	unknownIH := bytes.Repeat([]byte{0x22}, 20)
	alsoKnownIH := bytes.Repeat([]byte{0x33}, 20)

	g.set(pub, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: knownIH, N: "Known A"},
		{IH: unknownIH, N: "Unknown"},
		{IH: alsoKnownIH, N: "Known B"},
	}})

	bloom := reputation.NewBloomFilter(1000, 0.01)
	bloom.Add(knownIH)
	bloom.Add(alsoKnownIH)

	l := dhtindex.NewLookup(g)
	l.AddIndexer(pub, "idx")
	l.SetBloom(bloom)

	resp, err := l.Query(context.Background(), "debian")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Hits) != 3 {
		t.Fatalf("Hits = %d, want 3", len(resp.Hits))
	}

	bloomHits := 0
	for _, h := range resp.Hits {
		if h.BloomHit {
			bloomHits++
		}
	}
	if bloomHits != 2 {
		t.Errorf("bloomHits = %d, want 2", bloomHits)
	}

	// The two bloom-hit entries should sort before the non-bloom one.
	if !resp.Hits[0].BloomHit || !resp.Hits[1].BloomHit {
		t.Errorf("first two hits should be BloomHit=true, got [0]=%v [1]=%v",
			resp.Hits[0].BloomHit, resp.Hits[1].BloomHit)
	}
	if resp.Hits[2].BloomHit {
		t.Errorf("third hit should be BloomHit=false, got true")
	}
}

// TestLookupNoBloomMeansNoMarking verifies that without a bloom
// filter, all hits have BloomHit=false.
func TestLookupNoBloomMeansNoMarking(t *testing.T) {
	t.Parallel()
	g := newScriptedGetter()
	pub := newPubkey(t)
	salt, _ := dhtindex.SaltForKeyword("fedora")

	g.set(pub, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: bytes.Repeat([]byte{0x44}, 20), N: "Fedora"},
	}})

	l := dhtindex.NewLookup(g)
	l.AddIndexer(pub, "idx")
	// No bloom set.

	resp, err := l.Query(context.Background(), "fedora")
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range resp.Hits {
		if h.BloomHit {
			t.Errorf("hit %q has BloomHit=true without a bloom filter", h.Name)
		}
	}
}

// TestLookupBloomScoreBoost verifies that a bloom-hit entry
// receives a higher score than an otherwise identical non-bloom hit.
func TestLookupBloomScoreBoost(t *testing.T) {
	t.Parallel()
	g := newScriptedGetter()
	pub := newPubkey(t)
	salt, _ := dhtindex.SaltForKeyword("mint")

	bloomIH := bytes.Repeat([]byte{0xaa}, 20)
	normalIH := bytes.Repeat([]byte{0xbb}, 20)

	g.set(pub, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: normalIH, N: "Normal"},
		{IH: bloomIH, N: "Bloom Hit"},
	}})

	bloom := reputation.NewBloomFilter(1000, 0.01)
	bloom.Add(bloomIH)

	l := dhtindex.NewLookup(g)
	l.AddIndexer(pub, "idx")
	l.SetBloom(bloom)

	resp, err := l.Query(context.Background(), "mint")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Hits) != 2 {
		t.Fatalf("Hits = %d, want 2", len(resp.Hits))
	}

	var bloomScore, normalScore float64
	for _, h := range resp.Hits {
		if h.Name == "Bloom Hit" {
			bloomScore = h.Score
		} else {
			normalScore = h.Score
		}
	}
	if bloomScore <= normalScore {
		t.Errorf("bloom score %.3f should be > normal score %.3f", bloomScore, normalScore)
	}
}

// ---------------------------------------------------------------------------
// Lookup.Query source tracking integration
// ---------------------------------------------------------------------------

// TestLookupSourceTrackingRecords verifies that when a SourceTracker
// is attached, Query records every (infohash -> indexer) mapping.
func TestLookupSourceTrackingRecords(t *testing.T) {
	t.Parallel()
	g := newScriptedGetter()
	pub1 := newPubkey(t)
	pub2 := newPubkey(t)
	salt, _ := dhtindex.SaltForKeyword("opensuse")

	commonIH := bytes.Repeat([]byte{0x55}, 20)
	uniqueIH := bytes.Repeat([]byte{0x66}, 20)

	g.set(pub1, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: commonIH, N: "Common"},
		{IH: uniqueIH, N: "Unique"},
	}})
	g.set(pub2, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: commonIH, N: "Common"},
	}})

	sources := reputation.NewSourceTracker(100)

	l := dhtindex.NewLookup(g)
	l.AddIndexer(pub1, "idx-1")
	l.AddIndexer(pub2, "idx-2")
	l.SetSourceTracker(sources)

	if _, err := l.Query(context.Background(), "opensuse"); err != nil {
		t.Fatal(err)
	}

	// The common infohash should have both indexers as sources.
	commonHex := hex.EncodeToString(commonIH)
	commonSources := sources.Sources(commonHex)
	if len(commonSources) != 2 {
		t.Errorf("common sources = %d, want 2", len(commonSources))
	}

	// The unique infohash should have only pub1 as source.
	uniqueHex := hex.EncodeToString(uniqueIH)
	uniqueSources := sources.Sources(uniqueHex)
	if len(uniqueSources) != 1 {
		t.Errorf("unique sources = %d, want 1", len(uniqueSources))
	}
}

// TestLookupSourceTrackingNil verifies that a query with no
// SourceTracker does not panic.
func TestLookupSourceTrackingNil(t *testing.T) {
	t.Parallel()
	g := newScriptedGetter()
	pub := newPubkey(t)
	salt, _ := dhtindex.SaltForKeyword("alpine")
	g.set(pub, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: bytes.Repeat([]byte{0x77}, 20), N: "Alpine"},
	}})

	l := dhtindex.NewLookup(g)
	l.AddIndexer(pub, "idx")
	// No source tracker set.

	resp, err := l.Query(context.Background(), "alpine")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Hits) != 1 {
		t.Errorf("Hits = %d, want 1", len(resp.Hits))
	}
}

// ---------------------------------------------------------------------------
// Setter methods: SetTracker, SetBloom, SetSourceTracker, SetMinIndexerScore
// ---------------------------------------------------------------------------

// TestLookupSetterMethods exercises the setter/getter round-trip
// for each optional component attached to a Lookup.
func TestLookupSetterMethods(t *testing.T) {
	t.Parallel()
	l := dhtindex.NewLookup(newScriptedGetter())

	// Initially nil.
	if l.Tracker() != nil {
		t.Error("Tracker should be nil initially")
	}
	if l.Bloom() != nil {
		t.Error("Bloom should be nil initially")
	}

	// Set tracker.
	tracker := reputation.NewTracker()
	l.SetTracker(tracker)
	if l.Tracker() != tracker {
		t.Error("Tracker not set correctly")
	}

	// Detach tracker.
	l.SetTracker(nil)
	if l.Tracker() != nil {
		t.Error("Tracker should be nil after detach")
	}

	// Set bloom.
	bloom := reputation.NewBloomFilter(100, 0.01)
	l.SetBloom(bloom)
	if l.Bloom() != bloom {
		t.Error("Bloom not set correctly")
	}

	// Detach bloom.
	l.SetBloom(nil)
	if l.Bloom() != nil {
		t.Error("Bloom should be nil after detach")
	}
}

// TestLookupSetSourceTrackerRoundTrip sets a source tracker, runs a
// query, then detaches it and verifies no further recording.
func TestLookupSetSourceTrackerRoundTrip(t *testing.T) {
	t.Parallel()
	g := newScriptedGetter()
	pub := newPubkey(t)
	salt, _ := dhtindex.SaltForKeyword("slackware")
	g.set(pub, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: bytes.Repeat([]byte{0x88}, 20), N: "Slackware"},
	}})

	sources := reputation.NewSourceTracker(100)
	l := dhtindex.NewLookup(g)
	l.AddIndexer(pub, "idx")
	l.SetSourceTracker(sources)

	if _, err := l.Query(context.Background(), "slackware"); err != nil {
		t.Fatal(err)
	}
	if sources.Len() != 1 {
		t.Errorf("sources.Len() = %d after query, want 1", sources.Len())
	}

	// Detach tracker and query again with a different hit.
	l.SetSourceTracker(nil)
	ih2 := bytes.Repeat([]byte{0x89}, 20)
	salt2, _ := dhtindex.SaltForKeyword("nixos")
	g.set(pub, salt2, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: ih2, N: "NixOS"},
	}})
	if _, err := l.Query(context.Background(), "nixos"); err != nil {
		t.Fatal(err)
	}
	// Should still be 1 because the tracker was detached.
	if sources.Len() != 1 {
		t.Errorf("sources.Len() = %d after detach, want 1 (no new recording)", sources.Len())
	}
}

// ---------------------------------------------------------------------------
// SharedMemoryStore tests
// ---------------------------------------------------------------------------

// TestSharedMemoryStoreMultiPublisher verifies that two publishers
// can write to the same SharedMemoryStore under different keys,
// and a single Getter can read from both.
func TestSharedMemoryStoreMultiPublisher(t *testing.T) {
	t.Parallel()
	_, privA, _ := ed25519.GenerateKey(rand.Reader)
	_, privB, _ := ed25519.GenerateKey(rand.Reader)

	store := dhtindex.NewSharedMemoryStore()
	putA := store.PutterFor(privA)
	putB := store.PutterFor(privB)
	getter := store.Getter()

	salt, _ := dhtindex.SaltForKeyword("ubuntu")

	valA := dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: bytes.Repeat([]byte{0x01}, 20), N: "From A"},
	}}
	valB := dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: bytes.Repeat([]byte{0x02}, 20), N: "From B"},
	}}

	ctx := context.Background()
	if err := putA.Put(ctx, salt, valA); err != nil {
		t.Fatal(err)
	}
	if err := putB.Put(ctx, salt, valB); err != nil {
		t.Fatal(err)
	}

	// Items should have 2 entries.
	items := store.Items()
	if len(items) != 2 {
		t.Errorf("Items len = %d, want 2", len(items))
	}

	// Get each publisher's value independently.
	var pubA, pubB [32]byte
	copy(pubA[:], privA.Public().(ed25519.PublicKey))
	copy(pubB[:], privB.Public().(ed25519.PublicKey))

	gotA, err := getter.Get(ctx, pubA, salt)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotA.Hits) != 1 || gotA.Hits[0].N != "From A" {
		t.Errorf("gotA = %+v, want From A", gotA)
	}

	gotB, err := getter.Get(ctx, pubB, salt)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotB.Hits) != 1 || gotB.Hits[0].N != "From B" {
		t.Errorf("gotB = %+v, want From B", gotB)
	}
}

// TestSharedMemoryStoreGetterNotFound verifies the Getter returns an
// error for a (pubkey, salt) pair that was never published.
func TestSharedMemoryStoreGetterNotFound(t *testing.T) {
	t.Parallel()
	store := dhtindex.NewSharedMemoryStore()
	getter := store.Getter()

	var unknownPub [32]byte
	_, err := getter.Get(context.Background(), unknownPub, []byte("nothing"))
	if err == nil {
		t.Error("expected error for missing target")
	}
}

// TestSharedMemoryStoreOverwrite verifies that publishing under the
// same (pubkey, salt) pair overwrites the previous value.
func TestSharedMemoryStoreOverwrite(t *testing.T) {
	t.Parallel()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	store := dhtindex.NewSharedMemoryStore()
	putter := store.PutterFor(priv)
	getter := store.Getter()

	salt := []byte("test-salt")
	ctx := context.Background()

	v1 := dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: bytes.Repeat([]byte{0x01}, 20), N: "Version 1"},
	}}
	v2 := dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: bytes.Repeat([]byte{0x02}, 20), N: "Version 2"},
	}}

	if err := putter.Put(ctx, salt, v1); err != nil {
		t.Fatal(err)
	}
	if err := putter.Put(ctx, salt, v2); err != nil {
		t.Fatal(err)
	}

	var pub [32]byte
	copy(pub[:], priv.Public().(ed25519.PublicKey))
	got, err := getter.Get(ctx, pub, salt)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Hits) != 1 || got.Hits[0].N != "Version 2" {
		t.Errorf("got = %+v, want Version 2", got)
	}

	// Items should have exactly 1 entry (overwritten, not duplicated).
	if len(store.Items()) != 1 {
		t.Errorf("Items len = %d, want 1", len(store.Items()))
	}
}

// ---------------------------------------------------------------------------
// MemoryPutterGetter additional coverage
// ---------------------------------------------------------------------------

// TestMemoryPutterGetterItems verifies the Items() method returns
// all stored values.
func TestMemoryPutterGetterItems(t *testing.T) {
	t.Parallel()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	store := dhtindex.NewMemoryPutterGetter(priv)

	ctx := context.Background()
	salt1, _ := dhtindex.SaltForKeyword("linux")
	salt2, _ := dhtindex.SaltForKeyword("kernel")

	_ = store.Put(ctx, salt1, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: bytes.Repeat([]byte{0x01}, 20), N: "Linux"},
	}})
	_ = store.Put(ctx, salt2, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: bytes.Repeat([]byte{0x02}, 20), N: "Kernel"},
	}})

	items := store.Items()
	if len(items) != 2 {
		t.Errorf("Items len = %d, want 2", len(items))
	}
}

// TestMemoryPutterGetterNilPrivKey verifies that NewMemoryPutterGetter
// works with a nil private key (no signing, pure in-memory store).
func TestMemoryPutterGetterNilPrivKey(t *testing.T) {
	t.Parallel()
	store := dhtindex.NewMemoryPutterGetter(nil)

	ctx := context.Background()
	salt := []byte("test")
	err := store.Put(ctx, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: bytes.Repeat([]byte{0xab}, 20), N: "Test"},
	}})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Get with zero pubkey should find it (since nil priv → zero pub).
	var zeroPub [32]byte
	got, err := store.Get(ctx, zeroPub, salt)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Hits) != 1 || got.Hits[0].N != "Test" {
		t.Errorf("got = %+v, want one hit named 'Test'", got)
	}
}

// ---------------------------------------------------------------------------
// AnacrolixPutter/AnacrolixGetter constructor validation
// ---------------------------------------------------------------------------

// TestNewAnacrolixPutterNilServer verifies NewAnacrolixPutter rejects
// a nil DHT server.
func TestNewAnacrolixPutterNilServer(t *testing.T) {
	t.Parallel()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	_, err := dhtindex.NewAnacrolixPutter(nil, priv)
	if err == nil {
		t.Error("expected error for nil server")
	}
}

// TestNewAnacrolixPutterBadKey verifies NewAnacrolixPutter rejects
// an invalid private key.
func TestNewAnacrolixPutterBadKey(t *testing.T) {
	t.Parallel()
	_, err := dhtindex.NewAnacrolixPutter(nil, []byte("too-short"))
	if err == nil {
		t.Error("expected error for bad private key size")
	}
}

// TestNewAnacrolixGetterNilServer verifies NewAnacrolixGetter rejects
// a nil DHT server.
func TestNewAnacrolixGetterNilServer(t *testing.T) {
	t.Parallel()
	_, err := dhtindex.NewAnacrolixGetter(nil)
	if err == nil {
		t.Error("expected error for nil server")
	}
}

// ---------------------------------------------------------------------------
// Lookup score computation with combined tracker + bloom
// ---------------------------------------------------------------------------

// TestLookupScoreCombinedTrackerAndBloom verifies the full score
// computation pipeline: reputation from tracker + bloom boost.
func TestLookupScoreCombinedTrackerAndBloom(t *testing.T) {
	t.Parallel()
	g := newScriptedGetter()
	pub := newPubkey(t)
	salt, _ := dhtindex.SaltForKeyword("nixos")

	bloomIH := bytes.Repeat([]byte{0xaa}, 20)
	normalIH := bytes.Repeat([]byte{0xbb}, 20)

	g.set(pub, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: bloomIH, N: "Bloom+Rep"},
		{IH: normalIH, N: "Rep Only"},
	}})

	tracker := reputation.NewTracker()
	for i := 0; i < 50; i++ {
		tracker.RecordReturned(reputation.PubKey(pub), 1)
		tracker.RecordConfirmed(reputation.PubKey(pub))
	}

	bloom := reputation.NewBloomFilter(1000, 0.01)
	bloom.Add(bloomIH)

	l := dhtindex.NewLookup(g)
	l.AddIndexer(pub, "good-idx")
	l.SetTracker(tracker)
	l.SetBloom(bloom)

	resp, err := l.Query(context.Background(), "nixos")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Hits) != 2 {
		t.Fatalf("Hits = %d, want 2", len(resp.Hits))
	}

	var bloomScore, normalScore float64
	for _, h := range resp.Hits {
		switch h.Name {
		case "Bloom+Rep":
			bloomScore = h.Score
			if !h.BloomHit {
				t.Error("expected BloomHit=true for Bloom+Rep")
			}
		case "Rep Only":
			normalScore = h.Score
			if h.BloomHit {
				t.Error("expected BloomHit=false for Rep Only")
			}
		}
	}
	// Bloom hit should score higher.
	if bloomScore <= normalScore {
		t.Errorf("bloom score %.3f should be > normal score %.3f", bloomScore, normalScore)
	}
	// Both should be above the default 0.5 (good reputation).
	if normalScore <= 0.5 {
		t.Errorf("normal score %.3f should be > 0.5 (good reputation)", normalScore)
	}
}

// ---------------------------------------------------------------------------
// Lookup multi-source score bonus
// ---------------------------------------------------------------------------

// TestLookupMultiSourceScoreBonus verifies that hits seen by
// multiple indexers score higher than single-source hits.
func TestLookupMultiSourceScoreBonus(t *testing.T) {
	t.Parallel()
	g := newScriptedGetter()
	pub1 := newPubkey(t)
	pub2 := newPubkey(t)
	pub3 := newPubkey(t)
	salt, _ := dhtindex.SaltForKeyword("alpine")

	multiIH := bytes.Repeat([]byte{0xaa}, 20)
	singleIH := bytes.Repeat([]byte{0xbb}, 20)

	// All three indexers have multiIH; only pub1 has singleIH.
	g.set(pub1, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: multiIH, N: "Multi"},
		{IH: singleIH, N: "Single"},
	}})
	g.set(pub2, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: multiIH, N: "Multi"},
	}})
	g.set(pub3, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: multiIH, N: "Multi"},
	}})

	l := dhtindex.NewLookup(g)
	l.AddIndexer(pub1, "a")
	l.AddIndexer(pub2, "b")
	l.AddIndexer(pub3, "c")
	// No tracker — scores default to 0.5 per indexer.

	resp, err := l.Query(context.Background(), "alpine")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Hits) != 2 {
		t.Fatalf("Hits = %d, want 2", len(resp.Hits))
	}

	var multiScore, singleScore float64
	for _, h := range resp.Hits {
		switch h.Name {
		case "Multi":
			multiScore = h.Score
			if len(h.Sources) != 3 {
				t.Errorf("Multi sources = %d, want 3", len(h.Sources))
			}
		case "Single":
			singleScore = h.Score
			if len(h.Sources) != 1 {
				t.Errorf("Single sources = %d, want 1", len(h.Sources))
			}
		}
	}
	if multiScore <= singleScore {
		t.Errorf("multi-source score %.3f should be > single-source %.3f", multiScore, singleScore)
	}
}

// ---------------------------------------------------------------------------
// Lookup source tracking with multiple queries
// ---------------------------------------------------------------------------

// TestLookupSourceTrackingAccumulates verifies that running multiple
// queries accumulates source information in the tracker.
func TestLookupSourceTrackingAccumulates(t *testing.T) {
	t.Parallel()
	g := newScriptedGetter()
	pub1 := newPubkey(t)
	pub2 := newPubkey(t)

	ih := bytes.Repeat([]byte{0xcc}, 20)
	ihHex := hex.EncodeToString(ih)

	salt, _ := dhtindex.SaltForKeyword("redhat")
	g.set(pub1, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: ih, N: "RedHat"},
	}})

	sources := reputation.NewSourceTracker(100)
	l := dhtindex.NewLookup(g)
	l.AddIndexer(pub1, "first")
	l.SetSourceTracker(sources)

	// First query: only pub1 is an indexer.
	if _, err := l.Query(context.Background(), "redhat"); err != nil {
		t.Fatal(err)
	}
	s1 := sources.Sources(ihHex)
	if len(s1) != 1 {
		t.Errorf("after first query, sources = %d, want 1", len(s1))
	}

	// Add pub2 as indexer with the same hit and query again.
	g.set(pub2, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: ih, N: "RedHat"},
	}})
	l.AddIndexer(pub2, "second")

	if _, err := l.Query(context.Background(), "redhat"); err != nil {
		t.Fatal(err)
	}
	s2 := sources.Sources(ihHex)
	if len(s2) != 2 {
		t.Errorf("after second query, sources = %d, want 2", len(s2))
	}
}

// ---------------------------------------------------------------------------
// Lookup.Query hit merging edge cases
// ---------------------------------------------------------------------------

// TestLookupMergeNameFillIn verifies that if the first indexer
// returns a hit with an empty name and a later indexer returns the
// same hit with a non-empty name, the merged result uses the
// non-empty name.
func TestLookupMergeNameFillIn(t *testing.T) {
	t.Parallel()
	g := newScriptedGetter()
	pub1 := newPubkey(t)
	pub2 := newPubkey(t)
	salt, _ := dhtindex.SaltForKeyword("void")

	ih := bytes.Repeat([]byte{0xdd}, 20)
	g.set(pub1, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: ih, N: "", S: 10},
	}})
	g.set(pub2, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: ih, N: "Void Linux", S: 5},
	}})

	l := dhtindex.NewLookup(g)
	l.AddIndexer(pub1, "a")
	l.AddIndexer(pub2, "b")

	resp, err := l.Query(context.Background(), "void")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Hits) != 1 {
		t.Fatalf("Hits = %d, want 1", len(resp.Hits))
	}
	if resp.Hits[0].Name != "Void Linux" {
		t.Errorf("Name = %q, want 'Void Linux'", resp.Hits[0].Name)
	}
	// Seeders should be the max of the two (10).
	if resp.Hits[0].Seeders != 10 {
		t.Errorf("Seeders = %d, want 10", resp.Hits[0].Seeders)
	}
}

// TestLookupMergeSizeAndFileFillIn verifies that Size and Files
// fields are filled in from any indexer that provides them.
func TestLookupMergeSizeAndFileFillIn(t *testing.T) {
	t.Parallel()
	g := newScriptedGetter()
	pub1 := newPubkey(t)
	pub2 := newPubkey(t)
	salt, _ := dhtindex.SaltForKeyword("crux")

	ih := bytes.Repeat([]byte{0xee}, 20)
	g.set(pub1, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: ih, N: "CRUX", S: 5, Sz: 0, F: 0},
	}})
	g.set(pub2, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: ih, N: "CRUX", S: 3, Sz: 500 << 20, F: 7},
	}})

	l := dhtindex.NewLookup(g)
	l.AddIndexer(pub1, "a")
	l.AddIndexer(pub2, "b")

	resp, err := l.Query(context.Background(), "crux")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Hits) != 1 {
		t.Fatalf("Hits = %d, want 1", len(resp.Hits))
	}
	if resp.Hits[0].Size != 500<<20 {
		t.Errorf("Size = %d, want %d", resp.Hits[0].Size, 500<<20)
	}
	if resp.Hits[0].Files != 7 {
		t.Errorf("Files = %d, want 7", resp.Hits[0].Files)
	}
}

// ---------------------------------------------------------------------------
// Lookup tracker records HitsReturned for multiple indexers
// ---------------------------------------------------------------------------

// TestLookupRecordsHitsReturnedMultipleIndexers verifies that the
// tracker's HitsReturned counter is updated correctly for each
// responding indexer.
func TestLookupRecordsHitsReturnedMultipleIndexers(t *testing.T) {
	t.Parallel()
	g := newScriptedGetter()
	pub1 := newPubkey(t)
	pub2 := newPubkey(t)
	pub3 := newPubkey(t) // will be silent
	salt, _ := dhtindex.SaltForKeyword("solus")

	g.set(pub1, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: bytes.Repeat([]byte{0x01}, 20), N: "A"},
		{IH: bytes.Repeat([]byte{0x02}, 20), N: "B"},
		{IH: bytes.Repeat([]byte{0x03}, 20), N: "C"},
	}})
	g.set(pub2, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: bytes.Repeat([]byte{0x04}, 20), N: "D"},
	}})
	// pub3 has no data → will error → not counted.

	tracker := reputation.NewTracker()
	l := dhtindex.NewLookup(g)
	l.AddIndexer(pub1, "one")
	l.AddIndexer(pub2, "two")
	l.AddIndexer(pub3, "three")
	l.SetTracker(tracker)

	if _, err := l.Query(context.Background(), "solus"); err != nil {
		t.Fatal(err)
	}

	snap := tracker.Snapshot()
	// Sort by HitsReturned descending for stable assertions.
	sort.Slice(snap, func(i, j int) bool {
		return snap[i].Counters.HitsReturned > snap[j].Counters.HitsReturned
	})

	if len(snap) != 2 {
		t.Fatalf("tracker snapshot len = %d, want 2 (silent indexer not recorded)", len(snap))
	}
	if snap[0].Counters.HitsReturned != 3 {
		t.Errorf("first indexer HitsReturned = %d, want 3", snap[0].Counters.HitsReturned)
	}
	if snap[1].Counters.HitsReturned != 1 {
		t.Errorf("second indexer HitsReturned = %d, want 1", snap[1].Counters.HitsReturned)
	}
}

// ---------------------------------------------------------------------------
// Lookup.Query sorting (bloom > score > sources > name)
// ---------------------------------------------------------------------------

// TestLookupSortOrder verifies the full sort chain: bloom hits
// first, then by descending score, then by descending source count,
// then by ascending name.
func TestLookupSortOrder(t *testing.T) {
	t.Parallel()
	g := newScriptedGetter()
	pub := newPubkey(t)

	// Tokenize("popos distro") → ["popos", "distro"]; Query uses
	// the first token ("popos") as the keyword salt.
	salt, _ := dhtindex.SaltForKeyword("popos")

	// Give names to control tie-breaking.
	ihA := bytes.Repeat([]byte{0x01}, 20) // bloom hit
	ihB := bytes.Repeat([]byte{0x02}, 20) // no bloom
	ihC := bytes.Repeat([]byte{0x03}, 20) // no bloom

	g.set(pub, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: ihC, N: "Zulu"},  // sorts last by name
		{IH: ihB, N: "Alpha"}, // sorts first by name among non-bloom
		{IH: ihA, N: "Bravo"}, // bloom hit → sorts first overall
	}})

	bloom := reputation.NewBloomFilter(1000, 0.01)
	bloom.Add(ihA)

	l := dhtindex.NewLookup(g)
	l.AddIndexer(pub, "idx")
	l.SetBloom(bloom)

	resp, err := l.Query(context.Background(), "popos distro")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Hits) != 3 {
		t.Fatalf("Hits = %d, want 3", len(resp.Hits))
	}

	// First: Bravo (bloom hit).
	if resp.Hits[0].Name != "Bravo" {
		t.Errorf("first hit = %q, want Bravo (bloom hit)", resp.Hits[0].Name)
	}
	// Among non-bloom hits with equal score and sources, Alpha < Zulu.
	if resp.Hits[1].Name != "Alpha" {
		t.Errorf("second hit = %q, want Alpha", resp.Hits[1].Name)
	}
	if resp.Hits[2].Name != "Zulu" {
		t.Errorf("third hit = %q, want Zulu", resp.Hits[2].Name)
	}
}
