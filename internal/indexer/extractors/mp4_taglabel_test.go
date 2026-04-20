package extractors

import "testing"

// TestMP4TagLabelAllBranches drives every documented atom-type
// → label mapping in mp4TagLabel. The function is a pure switch
// statement over MP4/iTunes metadata atom IDs; previously only
// the handful exercised by the canned test file were covered.
func TestMP4TagLabelAllBranches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		atom string
		want string
	}{
		{"\xA9nam", "Title"},
		{"\xA9ART", "Artist"},
		{"aART", "Album artist"},
		{"\xA9alb", "Album"},
		{"\xA9day", "Date"},
		{"\xA9gen", "Genre"},
		{"gnre", "Genre"},
		{"\xA9wrt", "Composer"},
		{"\xA9too", "Encoder"},
		{"\xA9cmt", "Comment"},
		{"desc", "Description"},
		{"\xA9grp", "Grouping"},
		{"cprt", "Copyright"},
		{"trkn", "Track"},
		{"disk", "Disc"},
		// Default branch — unrecognised atom id returns "".
		{"xxxx", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := mp4TagLabel(c.atom); got != c.want {
			t.Errorf("mp4TagLabel(%q) = %q, want %q", c.atom, got, c.want)
		}
	}
}
