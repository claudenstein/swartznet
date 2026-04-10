package dhtindex_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/swartznet/swartznet/internal/dhtindex"
	"github.com/swartznet/swartznet/internal/reputation"
)

func newTestPubkey(t *testing.T) [32]byte {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var out [32]byte
	copy(out[:], pub)
	return out
}

// TestLookupBloomBoostSorts ensures that a hit whose infohash is
// in the user's known-good Bloom filter sorts above a hit that
// isn't, even if both come from the same indexer.
func TestLookupBloomBoostSorts(t *testing.T) {
	t.Parallel()
	g := newScriptedGetter()
	pubA := newTestPubkey(t)
	salt, _ := dhtindex.SaltForKeyword("ubuntu")

	knownIH := bytes.Repeat([]byte{0xaa}, 20)
	unknownIH := bytes.Repeat([]byte{0xbb}, 20)
	g.set(pubA, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: unknownIH, N: "Random unknown"},
		{IH: knownIH, N: "Trusted"},
	}})

	bloom := reputation.NewBloomFilter(1000, 0.01)
	bloom.Add(knownIH)

	l := dhtindex.NewLookup(g)
	l.AddIndexer(pubA, "indexer-a")
	l.SetBloom(bloom)

	resp, err := l.Query(context.Background(), "ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Hits) != 2 {
		t.Fatalf("hits len = %d, want 2", len(resp.Hits))
	}
	if !resp.Hits[0].BloomHit || resp.Hits[0].Name != "Trusted" {
		t.Errorf("first hit = %+v, want Trusted/BloomHit=true", resp.Hits[0])
	}
	if resp.Hits[1].BloomHit || resp.Hits[1].Name != "Random unknown" {
		t.Errorf("second hit = %+v, want Random unknown/BloomHit=false", resp.Hits[1])
	}
}

// TestLookupSkipsLowReputationIndexer verifies that a query
// silently skips indexers whose reputation falls below the
// configured cutoff.
func TestLookupSkipsLowReputationIndexer(t *testing.T) {
	t.Parallel()
	g := newScriptedGetter()
	good := newTestPubkey(t)
	bad := newTestPubkey(t)
	salt, _ := dhtindex.SaltForKeyword("debian")

	g.set(good, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: bytes.Repeat([]byte{0x10}, 20), N: "Good Hit"},
	}})
	g.set(bad, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: bytes.Repeat([]byte{0x20}, 20), N: "Spam"},
	}})

	tracker := reputation.NewTracker()
	// Pre-populate counters: good has 100 returned, all confirmed.
	// bad has 100 returned, all flagged.
	for i := 0; i < 100; i++ {
		tracker.RecordReturned(reputation.PubKey(good), 1)
		tracker.RecordConfirmed(reputation.PubKey(good))
		tracker.RecordReturned(reputation.PubKey(bad), 1)
		tracker.RecordFlagged(reputation.PubKey(bad))
	}

	l := dhtindex.NewLookup(g)
	l.AddIndexer(good, "good")
	l.AddIndexer(bad, "bad")
	l.SetTracker(tracker)
	l.SetMinIndexerScore(0.5)

	resp, err := l.Query(context.Background(), "debian")
	if err != nil {
		t.Fatal(err)
	}
	if resp.IndexersAsked != 1 {
		t.Errorf("IndexersAsked = %d, want 1 (bad indexer skipped)", resp.IndexersAsked)
	}
	if len(resp.Hits) != 1 || resp.Hits[0].Name != "Good Hit" {
		t.Errorf("hits = %+v, want only the good one", resp.Hits)
	}
}

// TestLookupRecordsHitsReturned verifies that running a query
// updates the tracker's counters for every responding indexer.
func TestLookupRecordsHitsReturned(t *testing.T) {
	t.Parallel()
	g := newScriptedGetter()
	pubA := newTestPubkey(t)
	salt, _ := dhtindex.SaltForKeyword("linux")
	g.set(pubA, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: bytes.Repeat([]byte{0x33}, 20), N: "Linux"},
		{IH: bytes.Repeat([]byte{0x34}, 20), N: "Linux Server"},
	}})

	tracker := reputation.NewTracker()
	l := dhtindex.NewLookup(g)
	l.AddIndexer(pubA, "")
	l.SetTracker(tracker)

	if _, err := l.Query(context.Background(), "linux"); err != nil {
		t.Fatal(err)
	}

	snap := tracker.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("tracker snapshot len = %d, want 1", len(snap))
	}
	if snap[0].Counters.HitsReturned != 2 {
		t.Errorf("HitsReturned = %d, want 2", snap[0].Counters.HitsReturned)
	}
}

// TestLookupScoreFavorsHighReputation verifies that when two
// indexers return the SAME infohash, the per-hit score reflects
// the average of the two indexers' reputation.
func TestLookupScoreFavorsHighReputation(t *testing.T) {
	t.Parallel()
	g := newScriptedGetter()
	high := newTestPubkey(t)
	mid := newTestPubkey(t)
	salt, _ := dhtindex.SaltForKeyword("ubuntu")

	common := bytes.Repeat([]byte{0x55}, 20)
	g.set(high, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: common, N: "Ubuntu"},
	}})
	g.set(mid, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: common, N: ""},
	}})
	// And a unique hit only the mid-rep indexer has.
	g.set(mid, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: common, N: ""},
		{IH: bytes.Repeat([]byte{0x66}, 20), N: "Mid-only"},
	}})

	tracker := reputation.NewTracker()
	for i := 0; i < 50; i++ {
		tracker.RecordReturned(reputation.PubKey(high), 1)
		tracker.RecordConfirmed(reputation.PubKey(high))
	}
	for i := 0; i < 50; i++ {
		tracker.RecordReturned(reputation.PubKey(mid), 1)
		// half confirmed, half not — mid score
		if i%2 == 0 {
			tracker.RecordConfirmed(reputation.PubKey(mid))
		}
	}

	l := dhtindex.NewLookup(g)
	l.AddIndexer(high, "high")
	l.AddIndexer(mid, "mid")
	l.SetTracker(tracker)

	resp, err := l.Query(context.Background(), "ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Hits) != 2 {
		t.Fatalf("hits len = %d, want 2", len(resp.Hits))
	}
	// The shared (high+mid) hit should have a higher score than
	// the mid-only one.
	var shared, midOnly float64
	for _, h := range resp.Hits {
		if h.Name == "Ubuntu" {
			shared = h.Score
		}
		if h.Name == "Mid-only" {
			midOnly = h.Score
		}
	}
	if shared <= midOnly {
		t.Errorf("shared score %.3f should be > mid-only %.3f", shared, midOnly)
	}
}
