package indexer_test

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/indexer"
)

// TestContentDocsForInfoHashPaginatesPastBatchSize covers the
// `from += batch` increment in ContentDocsForInfoHash at
// indexer.go:485. Index 1001 content docs under one infohash;
// the second iteration starts at from=1000 and the merged slice
// must include all 1001 entries.
func TestContentDocsForInfoHashPaginatesPastBatchSize(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("indexes 1001 content docs; takes ~10s")
	}
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "idx"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	const ih = "abababababababababababababababababababab"
	if err := idx.IndexTorrent(indexer.TorrentDoc{
		InfoHash: ih,
		Name:     "paginated",
	}); err != nil {
		t.Fatalf("IndexTorrent: %v", err)
	}

	const total = 1001
	for i := 0; i < total; i++ {
		if err := idx.IndexContent(indexer.ContentDoc{
			InfoHash:   ih,
			FileIndex:  i,
			ChunkIndex: 0,
			Text:       fmt.Sprintf("chunk-%d", i),
		}); err != nil {
			t.Fatalf("IndexContent %d: %v", i, err)
		}
	}

	docs, err := idx.ContentDocsForInfoHash(ih)
	if err != nil {
		t.Fatalf("ContentDocsForInfoHash: %v", err)
	}
	if len(docs) != total {
		t.Errorf("got %d docs, want %d", len(docs), total)
	}
}
