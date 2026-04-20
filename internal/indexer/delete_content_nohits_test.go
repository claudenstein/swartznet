package indexer_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/indexer"
)

// TestDeleteContentForTorrentReturnsZeroWhenAbsent covers the
// `len(res.Hits) == 0 → return deleted, nil` branch of
// deleteByQueryLocked. Ask to delete content for an infohash
// that has no content docs; the first Search returns zero hits
// and the function returns (0, nil).
func TestDeleteContentForTorrentReturnsZeroWhenAbsent(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "del.bleve"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	n, err := idx.DeleteContentForTorrent(strings.Repeat("a", 40))
	if err != nil {
		t.Fatalf("DeleteContentForTorrent: %v", err)
	}
	if n != 0 {
		t.Errorf("deleted = %d, want 0 for missing infohash", n)
	}
}
