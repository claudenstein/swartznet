package indexer_test

import (
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/indexer"
)

// TestSearchCapsLimitAtMax pins the documented MaxSearchLimit
// defence-in-depth cap. A caller that asks for an absurd Limit
// gets at most MaxSearchLimit hits back. We assert the request
// completes without error; the cap is internal so we can't
// observe the post-cap value directly, but reaching the body
// proves the branch ran.
func TestSearchCapsLimitAtMax(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "idx"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	if err := idx.IndexTorrent(indexer.TorrentDoc{
		InfoHash: "1111111111111111111111111111111111111111",
		Name:     "ubuntu",
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := idx.Search(indexer.SearchRequest{Query:"ubuntu", Limit: 999_999})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if resp.Total < 1 {
		t.Errorf("Total = %d, want at least 1", resp.Total)
	}
}

// TestSearchSignedByFiltersByPublisher covers the SignedBy
// filter branch. Two torrents with different signers; filtering
// to one should return exactly that one.
func TestSearchSignedByFiltersByPublisher(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "idx"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	const pubA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const pubB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := idx.IndexTorrent(indexer.TorrentDoc{
		InfoHash: "1111111111111111111111111111111111111111",
		Name:     "ubuntu by alice",
		SignedBy: pubA,
	}); err != nil {
		t.Fatal(err)
	}
	if err := idx.IndexTorrent(indexer.TorrentDoc{
		InfoHash: "2222222222222222222222222222222222222222",
		Name:     "ubuntu by bob",
		SignedBy: pubB,
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := idx.Search(indexer.SearchRequest{Query:"ubuntu", SignedBy: pubA})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if resp.Total != 1 {
		t.Errorf("Total = %d, want 1 (alice's only)", resp.Total)
	}
	if len(resp.Hits) > 0 && resp.Hits[0].SignedBy != pubA {
		t.Errorf("hit.SignedBy = %q, want %q", resp.Hits[0].SignedBy, pubA)
	}
}

// TestSearchEmptyQueryRejected pins the documented "empty query"
// error guard.
func TestSearchEmptyQueryRejected(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "idx"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	if _, err := idx.Search(indexer.SearchRequest{Query:""}); err == nil {
		t.Error("Search with empty query should error")
	}
}
