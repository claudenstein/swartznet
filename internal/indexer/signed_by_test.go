package indexer_test

import (
	"testing"

	"github.com/swartznet/swartznet/internal/indexer"
)

const (
	pubA = "a000000000000000000000000000000000000000000000000000000000000000"
	pubB = "b000000000000000000000000000000000000000000000000000000000000000"
)

// TestSearchSignedByFilter verifies that the SignedBy filter
// restricts results to torrents signed by the matching pubkey.
func TestSearchSignedByFilter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	idx, err := indexer.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	// Three docs: alice signed two, bob signed one, all matching
	// the word "ubuntu".
	docs := []indexer.TorrentDoc{
		{InfoHash: "1111111111111111111111111111111111111111", Name: "ubuntu desktop", SignedBy: pubA},
		{InfoHash: "2222222222222222222222222222222222222222", Name: "ubuntu server", SignedBy: pubA},
		{InfoHash: "3333333333333333333333333333333333333333", Name: "ubuntu derivatives", SignedBy: pubB},
	}
	for _, d := range docs {
		if err := idx.IndexTorrent(d); err != nil {
			t.Fatalf("IndexTorrent %s: %v", d.InfoHash, err)
		}
	}

	// No filter — all three.
	resp, err := idx.Search(indexer.SearchRequest{Query: "ubuntu"})
	if err != nil {
		t.Fatalf("Search all: %v", err)
	}
	if resp.Total != 3 {
		t.Errorf("unfiltered total: got %d, want 3", resp.Total)
	}

	// SignedBy = alice — two hits.
	resp, err = idx.Search(indexer.SearchRequest{Query: "ubuntu", SignedBy: pubA})
	if err != nil {
		t.Fatalf("Search alice: %v", err)
	}
	if resp.Total != 2 {
		t.Errorf("alice total: got %d, want 2", resp.Total)
	}
	for _, h := range resp.Hits {
		if h.SignedBy != pubA {
			t.Errorf("hit %s has SignedBy=%q, want %q", h.InfoHash, h.SignedBy, pubA)
		}
	}

	// SignedBy = bob — one hit.
	resp, err = idx.Search(indexer.SearchRequest{Query: "ubuntu", SignedBy: pubB})
	if err != nil {
		t.Fatalf("Search bob: %v", err)
	}
	if resp.Total != 1 {
		t.Errorf("bob total: got %d, want 1", resp.Total)
	}
}

// TestSearchSignedByPersistsThroughIndex verifies SignedBy
// round-trips the Bleve index.
func TestSearchSignedByPersistsThroughIndex(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	idx, err := indexer.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	if err := idx.IndexTorrent(indexer.TorrentDoc{
		InfoHash: "abcdef0000000000000000000000000000000000",
		Name:     "test",
		SignedBy: pubA,
	}); err != nil {
		t.Fatalf("IndexTorrent: %v", err)
	}
	resp, err := idx.Search(indexer.SearchRequest{Query: "test"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(resp.Hits) != 1 {
		t.Fatalf("got %d hits, want 1", len(resp.Hits))
	}
	if resp.Hits[0].SignedBy != pubA {
		t.Errorf("SignedBy round-trip failed: got %q, want %q", resp.Hits[0].SignedBy, pubA)
	}
}
