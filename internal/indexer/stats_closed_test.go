package indexer_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/indexer"
)

// TestStatsAfterCloseErrors covers the
// `i.bleve == nil → "indexer: closed"` defensive branch of
// Stats. Other tests exercise the populated/empty success paths.
func TestStatsAfterCloseErrors(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "closed.bleve"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := idx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	st, err := idx.Stats()
	if err == nil {
		t.Errorf("Stats on a closed index should error, got %+v", st)
	}
	if !strings.Contains(err.Error(), "closed") {
		t.Errorf("err = %q, want it to mention 'closed'", err.Error())
	}
}
