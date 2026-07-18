package indexer_test

import (
	"testing"

	"github.com/swartznet/swartznet/internal/indexer"
)

// TestPickLookupToken pins the single DHT token chooser (DECISIONS C16):
// most distinctive = longest by bytes, first-appearance tie-break,
// operating on the CAPPED Tokenize output so the chosen token is one
// publishers actually published.
func TestPickLookupToken(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		query string
		want  string
	}{
		// The legacy defect case: tokens[0] would have queried "new".
		{"most distinctive not first", "new ubuntu", "ubuntu"},
		{"single token", "debian", "debian"},
		{"longest wins", "go concurrency patterns", "concurrency"},
		{"tie broken by first appearance", "alpha bravo", "alpha"},
		{"stopwords and extensions filtered", "the ubuntu iso", "ubuntu"},
		{"empty query", "", ""},
		{"all stopwords", "the and for", ""},
		{"all too short", "a of x", ""},
		{
			// Nine distinct tokens: the cap keeps the first 8, so the
			// longest token overall (ninth) is never a candidate.
			"cap applies before choosing",
			"aaa bbb ccc ddd eee fff ggg hhh extraordinarilylongtoken",
			"aaa",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := indexer.PickLookupToken(tc.query); got != tc.want {
				t.Errorf("PickLookupToken(%q) = %q, want %q", tc.query, got, tc.want)
			}
		})
	}
}
