package indexer_test

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/indexer"
)

// TestStatsCorpusTextBytesCountsAllPages exercises the multi-page
// CorpusTextBytes scan after the guardTTL fix: with more than two
// batches (batch size 1000) of content docs, the scan must visit every
// page and sum every doc's text, terminating on the short final page
// rather than on a hardcoded page cap.
func TestStatsCorpusTextBytesCountsAllPages(t *testing.T) {
	if testing.Short() {
		t.Skip("indexes 2100 content docs; ~35s")
	}
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "idx"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	const docs = 2100 // > 2 batches => forces pagination past page 1
	const text = "lorem ipsum dolor sit amet"
	for i := 0; i < docs; i++ {
		if err := idx.IndexContent(indexer.ContentDoc{
			InfoHash:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			FileIndex: i, // distinct doc IDs
			FilePath:  fmt.Sprintf("f%d.txt", i),
			Text:      text,
		}); err != nil {
			t.Fatalf("IndexContent %d: %v", i, err)
		}
	}

	st, err := idx.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.ContentCount != uint64(docs) {
		t.Fatalf("ContentCount = %d, want %d", st.ContentCount, docs)
	}
	want := int64(docs) * int64(len(text))
	if st.CorpusTextBytes != want {
		t.Errorf("CorpusTextBytes = %d, want %d (scan truncated or miscounted)", st.CorpusTextBytes, want)
	}
}
