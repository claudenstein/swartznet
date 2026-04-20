package indexer_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/indexer"
)

// TestContentDocsForInfoHashAfterCloseErrors covers the
// `i.bleve == nil → "indexer: closed"` defensive branch of
// ContentDocsForInfoHash. Mirrors TestAllTorrentDocsAfterCloseErrors.
func TestContentDocsForInfoHashAfterCloseErrors(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "closed.bleve"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := idx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_, err = idx.ContentDocsForInfoHash(strings.Repeat("a", 40))
	if err == nil {
		t.Error("ContentDocsForInfoHash on a closed index should error")
	}
	if !strings.Contains(err.Error(), "closed") {
		t.Errorf("err = %q, want it to mention 'closed'", err.Error())
	}
}
