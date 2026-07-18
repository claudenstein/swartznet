package indexer_test

import (
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/indexer"
)

// openTempIndex is a tiny helper local to this file (the project
// already has openTempIndex in pipeline_test.go but with a slightly
// different return shape; keeping a private name avoids
// re-declaration).
func openGuardsIndex(t *testing.T) *indexer.Index {
	t.Helper()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "idx"))
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

func TestOpenWithLoggerRejectsEmptyPath(t *testing.T) {
	t.Parallel()
	if _, err := indexer.OpenWithLogger("", nil); err == nil {
		t.Error("OpenWithLogger should reject an empty path")
	}
}

func TestIndexTorrentRejectsEmptyInfoHash(t *testing.T) {
	t.Parallel()
	idx := openGuardsIndex(t)
	defer idx.Close()
	if err := idx.IndexTorrent(indexer.TorrentDoc{Name: "no infohash"}); err == nil {
		t.Error("IndexTorrent with empty InfoHash should error")
	}
}

func TestIndexContentRejectsEmptyInfoHash(t *testing.T) {
	t.Parallel()
	idx := openGuardsIndex(t)
	defer idx.Close()
	doc := indexer.ContentDoc{Text: "hello"}
	if err := idx.IndexContent(doc); err == nil {
		t.Error("IndexContent with empty InfoHash should error")
	}
}

func TestIndexContentRejectsEmptyText(t *testing.T) {
	t.Parallel()
	idx := openGuardsIndex(t)
	defer idx.Close()
	doc := indexer.ContentDoc{InfoHash: "1111111111111111111111111111111111111111"}
	if err := idx.IndexContent(doc); err == nil {
		t.Error("IndexContent with empty Text should error")
	}
}

// TestPostCloseGuards verifies that operations on a closed index
// return the documented "indexer: closed" error rather than
// panicking on the nil bleve handle. Covers the closed-index
// branch of IndexTorrent, IndexContent, DeleteTorrent,
// DeleteContentForTorrent, and DocCount in one closed lifecycle.
func TestPostCloseGuards(t *testing.T) {
	t.Parallel()
	idx := openGuardsIndex(t)
	if err := idx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := idx.IndexTorrent(indexer.TorrentDoc{
		InfoHash: "1111111111111111111111111111111111111111",
		Name:     "after-close",
	}); err == nil {
		t.Error("IndexTorrent on closed index should error")
	}

	if err := idx.IndexContent(indexer.ContentDoc{
		InfoHash: "1111111111111111111111111111111111111111",
		Text:     "after-close",
	}); err == nil {
		t.Error("IndexContent on closed index should error")
	}

	if err := idx.DeleteTorrent("1111111111111111111111111111111111111111"); err == nil {
		t.Error("DeleteTorrent on closed index should error")
	}

	if _, err := idx.DeleteContentForTorrent("1111111111111111111111111111111111111111"); err == nil {
		t.Error("DeleteContentForTorrent on closed index should error")
	}

	if _, err := idx.DocCount(); err == nil {
		t.Error("DocCount on closed index should error")
	}
}
