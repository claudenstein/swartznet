package indexer_test

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/indexer"
)

// TestStatsContentDocPaginationIncrement covers Stats's
// `from += batch` increment in the content-text scan loop at
// indexer.go:359. Uses the same 1000-doc batch as AllTorrentDocs;
// indexing 1001 content docs forces a second iteration with
// from=1000. textSum must equal the total text length.
func TestStatsContentDocPaginationIncrement(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("indexes 1001 content docs; takes ~10s")
	}
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "idx"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	const total = 1001
	const text = "page text"
	for i := 0; i < total; i++ {
		ih := fmt.Sprintf("%040d", i+1)
		// Pre-register the torrent so foreign-key-style implicit
		// joins (none in Bleve, but conceptually consistent) hold.
		if err := idx.IndexTorrent(indexer.TorrentDoc{
			InfoHash: ih,
			Name:     fmt.Sprintf("t-%d", i),
		}); err != nil {
			t.Fatalf("IndexTorrent %d: %v", i, err)
		}
		if err := idx.IndexContent(indexer.ContentDoc{
			InfoHash:   ih,
			FileIndex:  0,
			ChunkIndex: 0,
			Text:       text,
		}); err != nil {
			t.Fatalf("IndexContent %d: %v", i, err)
		}
	}

	st, err := idx.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.ContentCount != total {
		t.Errorf("ContentCount = %d, want %d", st.ContentCount, total)
	}
	wantBytes := int64(len(text) * total)
	if st.CorpusTextBytes != wantBytes {
		t.Errorf("CorpusTextBytes = %d, want %d", st.CorpusTextBytes, wantBytes)
	}
}
