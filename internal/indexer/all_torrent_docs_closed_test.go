package indexer_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/indexer"
)

// TestAllTorrentDocsAfterCloseErrors covers the
// `i.bleve == nil → "indexer: closed"` defensive branch of
// AllTorrentDocs. The other tests exercise empty / single /
// multiple indexes; this fills in the closed-index error path.
func TestAllTorrentDocsAfterCloseErrors(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "closed.bleve"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := idx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_, err = idx.AllTorrentDocs()
	if err == nil {
		t.Error("AllTorrentDocs on a closed index should error")
	}
	if !strings.Contains(err.Error(), "closed") {
		t.Errorf("err = %q, want it to mention 'closed'", err.Error())
	}
}
