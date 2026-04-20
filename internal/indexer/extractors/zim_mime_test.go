package extractors

import "testing"

// TestZimIsExtractableMimeAllBranches drives every documented
// branch of zimIsExtractableMime. The function decides whether a
// ZIM blob's MIME is convertible to searchable plaintext; the
// table pins the exact contract for each prefix family plus the
// negative fall-through.
func TestZimIsExtractableMimeAllBranches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		mime string
		want bool
	}{
		// text/html prefix branch (with and without charset suffix).
		{"text/html", true},
		{"text/html; charset=utf-8", true},
		// application/xhtml prefix branch.
		{"application/xhtml+xml", true},
		{"application/xhtml", true},
		// text/plain prefix branch.
		{"text/plain", true},
		{"text/plain; charset=utf-8", true},
		// Default fall-through — MIME types we deliberately ignore.
		{"image/png", false},
		{"application/octet-stream", false},
		{"audio/mpeg", false},
		{"", false},
	}
	for _, c := range cases {
		if got := zimIsExtractableMime(c.mime); got != c.want {
			t.Errorf("zimIsExtractableMime(%q) = %v, want %v", c.mime, got, c.want)
		}
	}
}
