package indexer_test

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/indexer"
)

// TestAllTorrentDocsPaginatesPastBatchSize covers the
// `from += batch` increment in AllTorrentDocs. The function
// pages through results in 1000-doc batches; with > 1000
// torrent docs in the index, the second iteration runs with
// from=1000. Index 1001 docs and assert the returned slice
// contains all of them.
func TestAllTorrentDocsPaginatesPastBatchSize(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("indexes 1001 docs; takes ~20s")
	}
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "idx"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	const total = 1001
	for i := 0; i < total; i++ {
		ih := fmt.Sprintf("%040d", i+1)
		if err := idx.IndexTorrent(indexer.TorrentDoc{
			InfoHash: ih,
			Name:     fmt.Sprintf("torrent-%d", i),
		}); err != nil {
			t.Fatalf("IndexTorrent %d: %v", i, err)
		}
	}

	docs, err := idx.AllTorrentDocs()
	if err != nil {
		t.Fatalf("AllTorrentDocs: %v", err)
	}
	if len(docs) != total {
		t.Errorf("AllTorrentDocs returned %d docs, want %d", len(docs), total)
	}
}
