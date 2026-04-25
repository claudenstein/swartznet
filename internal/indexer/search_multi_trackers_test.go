package indexer_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/indexer"
)

// TestSearchHitMultipleTrackers covers the
// `case []any: for _, t := range v ...` arm of Index.Search's
// fieldTrackers projection. A torrent with two trackers makes
// Bleve return the field as a slice rather than a string, so
// the type-switch's slice arm fires (the single-tracker case
// returns a string and is already covered).
func TestSearchHitMultipleTrackers(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "multi.bleve")
	idx, err := indexer.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	doc := indexer.TorrentDoc{
		InfoHash: "1111111111111111111111111111111111111111",
		Name:     "ubuntu twin-tracker",
		Trackers: []string{
			"udp://tracker.alpha.example:6969/announce",
			"udp://tracker.beta.example:6881/announce",
		},
		SizeBytes: 1024,
		FileCount: 1,
		AddedAt:   time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC),
	}
	if err := idx.IndexTorrent(doc); err != nil {
		t.Fatalf("IndexTorrent: %v", err)
	}

	res, err := idx.Search(indexer.SearchRequest{Query: "ubuntu", Limit: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Hits) == 0 {
		t.Fatal("Search returned no hits for indexed multi-tracker doc")
	}
	hit := res.Hits[0]
	if len(hit.Trackers) != 2 {
		t.Errorf("hit.Trackers len = %d, want 2", len(hit.Trackers))
	}
}
