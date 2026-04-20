package indexer_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/indexer"
)

// TestSearchAfterCloseErrors covers the
// `i.bleve == nil → "indexer: closed"` defensive branch of
// Search. The other Search tests exercise query parsing, limit
// caps, highlighting, and SignedBy filtering on a live index.
func TestSearchAfterCloseErrors(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "closed.bleve"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := idx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	res, err := idx.Search(indexer.SearchRequest{Query: "anything"})
	if err == nil {
		t.Errorf("Search on a closed index should error, got %+v", res)
	}
	if !strings.Contains(err.Error(), "closed") {
		t.Errorf("err = %q, want it to mention 'closed'", err.Error())
	}
}
