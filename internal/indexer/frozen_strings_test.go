package indexer_test

import (
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/indexer"
)

// TestFrozenErrorStrings pins the contract's exact error strings
// byte-for-byte — callers and operators grep for them.
func TestFrozenErrorStrings(t *testing.T) {
	t.Parallel()

	if _, err := indexer.Open(""); err == nil || err.Error() != "indexer: path must not be empty" {
		t.Errorf("Open(\"\") err = %v", err)
	}

	idx, err := indexer.Open(filepath.Join(t.TempDir(), "idx"))
	if err != nil {
		t.Fatal(err)
	}

	if err := idx.IndexTorrent(indexer.TorrentDoc{}); err == nil ||
		err.Error() != "indexer: TorrentDoc.InfoHash must not be empty" {
		t.Errorf("IndexTorrent(empty ih) err = %v", err)
	}
	if err := idx.IndexContent(indexer.ContentDoc{Text: "x"}); err == nil ||
		err.Error() != "indexer: ContentDoc.InfoHash must not be empty" {
		t.Errorf("IndexContent(empty ih) err = %v", err)
	}
	if err := idx.IndexContent(indexer.ContentDoc{
		InfoHash: "1111111111111111111111111111111111111111",
	}); err == nil || err.Error() != "indexer: ContentDoc.Text must not be empty" {
		t.Errorf("IndexContent(empty text) err = %v", err)
	}
	if _, err := idx.Search(indexer.SearchRequest{}); err == nil ||
		err.Error() != "indexer: empty query" {
		t.Errorf("Search(empty) err = %v", err)
	}

	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := idx.DocCount(); err == nil || err.Error() != "indexer: closed" {
		t.Errorf("DocCount(closed) err = %v", err)
	}
}

// TestFrozenConstants pins the contract's numeric constants.
func TestFrozenConstants(t *testing.T) {
	t.Parallel()
	if indexer.SchemaVersion != 3 {
		t.Errorf("SchemaVersion = %d, want 3", indexer.SchemaVersion)
	}
	if indexer.MaxSearchLimit != 10_000 {
		t.Errorf("MaxSearchLimit = %d, want 10000", indexer.MaxSearchLimit)
	}
}
