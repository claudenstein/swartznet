package dhtindex_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"testing"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// scriptedGetter returns a fixed value for a specific (pubkey, salt)
// pair, and "not found" for everything else.
type scriptedGetter struct {
	mu      sync.Mutex
	entries map[string]dhtindex.KeywordValue
}

func newScriptedGetter() *scriptedGetter {
	return &scriptedGetter{entries: make(map[string]dhtindex.KeywordValue)}
}

func (s *scriptedGetter) set(pubkey [32]byte, salt []byte, v dhtindex.KeywordValue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[string(pubkey[:])+"|"+string(salt)] = v
}

func (s *scriptedGetter) Get(ctx context.Context, pubkey [32]byte, salt []byte) (dhtindex.KeywordValue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.entries[string(pubkey[:])+"|"+string(salt)]
	if !ok {
		return dhtindex.KeywordValue{}, context.DeadlineExceeded
	}
	return v, nil
}

func newPubkey(t *testing.T) [32]byte {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var out [32]byte
	copy(out[:], pub)
	return out
}

func TestLookupNoIndexers(t *testing.T) {
	t.Parallel()
	g := newScriptedGetter()
	l := dhtindex.NewLookup(g)
	resp, err := l.Query(context.Background(), "ubuntu")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if resp.IndexersAsked != 0 || len(resp.Hits) != 0 {
		t.Errorf("expected empty response, got %+v", resp)
	}
}

func TestLookupAddIndexerHex(t *testing.T) {
	t.Parallel()
	l := dhtindex.NewLookup(newScriptedGetter())
	pub := newPubkey(t)
	hexKey := hex.EncodeToString(pub[:])
	if err := l.AddIndexerHex(hexKey, "test-seed"); err != nil {
		t.Fatal(err)
	}
	if len(l.Indexers()) != 1 {
		t.Errorf("len(Indexers) = %d, want 1", len(l.Indexers()))
	}
	if err := l.AddIndexerHex("not-hex", ""); err == nil {
		t.Error("expected error for malformed hex key")
	}
	if err := l.AddIndexerHex("aabb", ""); err == nil {
		t.Error("expected error for too-short hex key")
	}
}

func TestLookupQueryFanoutAndMerge(t *testing.T) {
	t.Parallel()
	g := newScriptedGetter()

	pub1 := newPubkey(t)
	pub2 := newPubkey(t)
	pub3 := newPubkey(t)

	salt, _ := dhtindex.SaltForKeyword("ubuntu")

	// Indexer 1 and 2 both know about infohash 0xaa.
	g.set(pub1, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: bytes.Repeat([]byte{0xaa}, 20), N: "Ubuntu 24.04", S: 100, Sz: 6 << 30},
	}})
	g.set(pub2, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: bytes.Repeat([]byte{0xaa}, 20), N: "", S: 130},
	}})
	// Indexer 3 has a different torrent.
	g.set(pub3, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: bytes.Repeat([]byte{0xbb}, 20), N: "Ubuntu Server", S: 50},
	}})

	l := dhtindex.NewLookup(g)
	l.AddIndexer(pub1, "indexer-one")
	l.AddIndexer(pub2, "indexer-two")
	l.AddIndexer(pub3, "indexer-three")

	resp, err := l.Query(context.Background(), "ubuntu")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if resp.IndexersAsked != 3 {
		t.Errorf("IndexersAsked = %d, want 3", resp.IndexersAsked)
	}
	if resp.IndexersResponded != 3 {
		t.Errorf("IndexersResponded = %d, want 3", resp.IndexersResponded)
	}
	if len(resp.Hits) != 2 {
		t.Fatalf("Hits = %d, want 2 (dedup by infohash)", len(resp.Hits))
	}
	// First hit is the most-sourced one (0xaa, 2 sources).
	if !strings.HasPrefix(resp.Hits[0].InfoHash, "aaaa") {
		t.Errorf("first hit = %q, want one starting with aaaa", resp.Hits[0].InfoHash)
	}
	if len(resp.Hits[0].Sources) != 2 {
		t.Errorf("first hit Sources = %v, want 2 entries", resp.Hits[0].Sources)
	}
	// Name should come from the indexer that supplied a non-empty
	// name (pub1, "Ubuntu 24.04"); seeders should be the max (130).
	if resp.Hits[0].Name != "Ubuntu 24.04" {
		t.Errorf("merged name = %q, want 'Ubuntu 24.04'", resp.Hits[0].Name)
	}
	if resp.Hits[0].Seeders != 130 {
		t.Errorf("merged seeders = %d, want 130", resp.Hits[0].Seeders)
	}
}

// TestLookupQuerySortTiesByOnSourcesCount covers the
// `if len(resp.Hits[i].Sources) != len(resp.Hits[j].Sources)`
// tie-breaker arm at lookup.go:478-480. The bonus on
// scoreLookupHit saturates at 5 sources (0.05*(N-1) capped at
// 0.20), so two infohashes — one with 5 sources, one with 6 —
// produce identical Score. The sort then falls through to the
// Sources-count tie-breaker, putting the 6-sourced hit first.
func TestLookupQuerySortTiesByOnSourcesCount(t *testing.T) {
	t.Parallel()
	g := newScriptedGetter()
	salt, _ := dhtindex.SaltForKeyword("alpha")

	// 6 indexers all carry hit 0xaa; 5 of those 6 also carry 0xbb.
	// Both hits saturate the +0.20 multi-source bonus, so the
	// Score field is equal between them. The tie-breaker drops to
	// Sources count: 6 wins over 5.
	pubs := make([][32]byte, 6)
	for i := range pubs {
		pubs[i] = newPubkey(t)
	}
	for i, pk := range pubs {
		hits := []dhtindex.KeywordHit{
			{IH: bytes.Repeat([]byte{0xaa}, 20), N: "alpha-aa"},
		}
		if i < 5 {
			hits = append(hits, dhtindex.KeywordHit{
				IH: bytes.Repeat([]byte{0xbb}, 20), N: "alpha-bb",
			})
		}
		g.set(pk, salt, dhtindex.KeywordValue{Hits: hits})
	}

	l := dhtindex.NewLookup(g)
	for _, pk := range pubs {
		l.AddIndexer(pk, "")
	}

	resp, err := l.Query(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(resp.Hits) != 2 {
		t.Fatalf("Hits = %d, want 2", len(resp.Hits))
	}
	if resp.Hits[0].Score != resp.Hits[1].Score {
		t.Errorf("scores differ: %v vs %v — tie-breaker arm not exercised",
			resp.Hits[0].Score, resp.Hits[1].Score)
	}
	if len(resp.Hits[0].Sources) <= len(resp.Hits[1].Sources) {
		t.Errorf("first hit Sources = %d, second = %d; want first > second",
			len(resp.Hits[0].Sources), len(resp.Hits[1].Sources))
	}
}

func TestLookupSomeIndexersSilent(t *testing.T) {
	t.Parallel()
	g := newScriptedGetter()
	pub1 := newPubkey(t)
	pub2 := newPubkey(t)
	salt, _ := dhtindex.SaltForKeyword("debian")

	// Only pub1 has data; pub2 is silent (no entry → "not found"
	// from the scripted getter).
	g.set(pub1, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: bytes.Repeat([]byte{0xcc}, 20), N: "Debian Bookworm"},
	}})

	l := dhtindex.NewLookup(g)
	l.AddIndexer(pub1, "")
	l.AddIndexer(pub2, "")

	resp, err := l.Query(context.Background(), "debian")
	if err != nil {
		t.Fatal(err)
	}
	if resp.IndexersAsked != 2 {
		t.Errorf("IndexersAsked = %d, want 2", resp.IndexersAsked)
	}
	if resp.IndexersResponded != 1 {
		t.Errorf("IndexersResponded = %d, want 1", resp.IndexersResponded)
	}
	if len(resp.Hits) != 1 {
		t.Errorf("Hits len = %d, want 1", len(resp.Hits))
	}
}

// TestLookupQueryDropsBadInfoHashLength covers the
// `if len(ih) != 40 { continue }` arm of legacyQuery's merge
// loop. KeywordHit.IH is a []byte so a malformed indexer can
// publish a hit with an IH that's not exactly 20 bytes; the
// merge must skip it without indexing into the empty slot.
func TestLookupQueryDropsBadInfoHashLength(t *testing.T) {
	t.Parallel()
	g := newScriptedGetter()
	pub := newPubkey(t)
	salt, _ := dhtindex.SaltForKeyword("ubuntu")
	g.set(pub, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		// 19-byte IH → hex string is 38 chars → skipped.
		{IH: bytes.Repeat([]byte{0xaa}, 19), N: "Wrong Length", S: 50},
		// Valid 20-byte IH → kept.
		{IH: bytes.Repeat([]byte{0xbb}, 20), N: "Right Length", S: 100},
	}})

	l := dhtindex.NewLookup(g)
	l.AddIndexer(pub, "indexer-one")
	resp, err := l.Query(context.Background(), "ubuntu")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(resp.Hits) != 1 {
		t.Fatalf("Hits = %d, want 1 (bad-length one dropped)", len(resp.Hits))
	}
	if resp.Hits[0].Name != "Right Length" {
		t.Errorf("kept hit name = %q, want 'Right Length'", resp.Hits[0].Name)
	}
}

// TestLookupQueryMergeFillsEmptyNameWithinIndexer covers the
// `if lh.Name == "" && h.N != "" { lh.Name = h.N }` arm and
// the matching Size/Files fill-from-later-hit arms of
// legacyQuery's merge loop. A single indexer returns two hits
// for the same infohash where the first has empty Name/Size/
// Files and the second has non-empty values for all three.
// The merge processes hits in slice order, so the second
// hit's metadata deterministically fills the initially-empty
// merged entry.
func TestLookupQueryMergeFillsEmptyNameWithinIndexer(t *testing.T) {
	t.Parallel()
	g := newScriptedGetter()
	pub := newPubkey(t)
	salt, _ := dhtindex.SaltForKeyword("ubuntu")
	g.set(pub, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{
		{IH: bytes.Repeat([]byte{0xaa}, 20), N: "", S: 50, Sz: 0, F: 0},
		{IH: bytes.Repeat([]byte{0xaa}, 20), N: "Ubuntu Late", S: 100, Sz: 6 << 30, F: 12},
	}})

	l := dhtindex.NewLookup(g)
	l.AddIndexer(pub, "indexer-one")
	resp, err := l.Query(context.Background(), "ubuntu")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(resp.Hits) != 1 {
		t.Fatalf("Hits = %d, want 1 (dedup)", len(resp.Hits))
	}
	hit := resp.Hits[0]
	if hit.Name != "Ubuntu Late" {
		t.Errorf("merged name = %q, want 'Ubuntu Late'", hit.Name)
	}
	if hit.Size != 6<<30 {
		t.Errorf("merged Size = %d, want %d (filled from later hit)", hit.Size, int64(6<<30))
	}
	if hit.Files != 12 {
		t.Errorf("merged Files = %d, want 12 (filled from later hit)", hit.Files)
	}
}

func TestLookupQueryEmptyTokens(t *testing.T) {
	t.Parallel()
	l := dhtindex.NewLookup(newScriptedGetter())
	pub := newPubkey(t)
	l.AddIndexer(pub, "")
	// "the" is a stopword and "of" is too short — no tokens survive.
	_, err := l.Query(context.Background(), "the of")
	if err == nil {
		t.Error("expected error for query that produces no tokens")
	}
}

func TestLookupRemoveIndexer(t *testing.T) {
	t.Parallel()
	l := dhtindex.NewLookup(newScriptedGetter())
	pub := newPubkey(t)
	l.AddIndexer(pub, "")
	if len(l.Indexers()) != 1 {
		t.Fatal("expected 1 indexer after add")
	}
	l.RemoveIndexer(pub)
	if len(l.Indexers()) != 0 {
		t.Errorf("expected 0 indexers after remove, got %d", len(l.Indexers()))
	}
}
