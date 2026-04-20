package extractors

import "testing"

// TestVorbisLabelKnownFields covers every exact-match case of the
// vorbisLabel switch, plus the synonym pairs (DATE/YEAR,
// DESCRIPTION/COMMENT, ORGANIZATION/LABEL).
func TestVorbisLabelKnownFields(t *testing.T) {
	t.Parallel()

	cases := []struct {
		field, want string
	}{
		{"TITLE", "Title"},
		{"ARTIST", "Artist"},
		{"PERFORMER", "Performer"},
		{"ALBUM", "Album"},
		{"ALBUMARTIST", "Album artist"},
		{"DATE", "Date"},
		{"YEAR", "Date"},
		{"GENRE", "Genre"},
		{"TRACKNUMBER", "Track"},
		{"DISCNUMBER", "Disc"},
		{"COMPOSER", "Composer"},
		{"DESCRIPTION", "Comment"},
		{"COMMENT", "Comment"},
		{"ORGANIZATION", "Label"},
		{"LABEL", "Label"},
		{"ISRC", "ISRC"},
		{"COPYRIGHT", "Copyright"},
	}

	for _, tc := range cases {
		if got := vorbisLabel(tc.field); got != tc.want {
			t.Errorf("vorbisLabel(%q) = %q, want %q", tc.field, got, tc.want)
		}
	}
}

func TestVorbisLabelUnknownReturnsEmpty(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"", "title", "BPM", "REPLAYGAIN_TRACK_GAIN"} {
		if got := vorbisLabel(name); got != "" {
			t.Errorf("vorbisLabel(%q) = %q, want empty", name, got)
		}
	}
}
