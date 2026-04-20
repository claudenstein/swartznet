package companion_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/companion"
	"github.com/swartznet/swartznet/internal/indexer"
)

// TestBuildFromIndexAllTorrentDocsError covers the
// "AllTorrentDocs returned an error" branch of BuildFromIndex.
// Open then close an index so AllTorrentDocs returns
// "indexer: closed", and assert the wrapped "list torrents"
// error propagates.
func TestBuildFromIndexAllTorrentDocsError(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "closed.bleve"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := idx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_, err = companion.BuildFromIndex(idx, "pubkey", companion.DefaultBuildOptions())
	if err == nil {
		t.Fatal("BuildFromIndex on closed index should error")
	}
	if !strings.Contains(err.Error(), "list torrents") {
		t.Errorf("err = %q, want it to wrap 'list torrents'", err.Error())
	}
}
