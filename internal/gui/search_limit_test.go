package gui

import "testing"

// TestParseSearchLimit covers the strict Limit-entry parsing. The
// old Sscanf-based parse let negative values and leading-digits-
// plus-garbage through to the search layers; every malformed input
// must now fall back to the default of 20.
func TestParseSearchLimit(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"empty falls back", "", 20},
		{"whitespace only falls back", "   ", 20},
		{"plain positive", "5", 5},
		{"trimmed positive", " 42 ", 42},
		{"zero falls back", "0", 20},
		{"negative falls back", "-3", 20},
		{"garbage falls back", "abc", 20},
		{"trailing garbage falls back", "5; rm", 20},
		{"float falls back", "3.5", 20},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseSearchLimit(c.in); got != c.want {
				t.Errorf("parseSearchLimit(%q) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}
