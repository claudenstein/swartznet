package indexer_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/indexer"
)

// TestSearchMalformedQueryIsBadQuery pins the client-vs-server classification:
// a query string that is not valid Bleve syntax must surface as the typed
// ErrBadQuery (so API boundaries can answer 400), NOT as a generic Search
// failure (which would become a 500). The index itself is healthy.
func TestSearchMalformedQueryIsBadQuery(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "idx"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	if err := idx.IndexTorrent(indexer.TorrentDoc{
		InfoHash: "1111111111111111111111111111111111111111",
		Name:     "ubuntu",
	}); err != nil {
		t.Fatal(err)
	}

	// Bleve's query-string parser rejects each of these (unbalanced paren,
	// dangling boost, empty numeric range). Any one is a caller error.
	for _, q := range []string{"(unbalanced", "title:>", "^", "foo:(bar"} {
		_, err := idx.Search(indexer.SearchRequest{Query: q})
		if err == nil {
			// Not every string bleve tolerates is worth failing on; only assert
			// classification for the ones it actually rejects.
			continue
		}
		if !errors.Is(err, indexer.ErrBadQuery) {
			t.Errorf("Search(%q) error = %v, want ErrBadQuery", q, err)
		}
	}

	// A well-formed query still succeeds (the guard did not over-reject).
	if _, err := idx.Search(indexer.SearchRequest{Query: "ubuntu"}); err != nil {
		t.Fatalf("valid query rejected: %v", err)
	}
}

// TestSearchAtLeastOneMalformedRejected guards against the parser silently
// accepting everything (which would make the classification untestable): at
// least one of the candidate malformed strings must be rejected as ErrBadQuery.
func TestSearchAtLeastOneMalformedRejected(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "idx"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	var sawBadQuery bool
	for _, q := range []string{"(unbalanced", "title:>", "^", "foo:(bar", "a AND OR b", "["} {
		if _, err := idx.Search(indexer.SearchRequest{Query: q}); errors.Is(err, indexer.ErrBadQuery) {
			sawBadQuery = true
			break
		}
	}
	if !sawBadQuery {
		t.Fatal("no candidate malformed query was classified ErrBadQuery — the guard never fires")
	}
}
