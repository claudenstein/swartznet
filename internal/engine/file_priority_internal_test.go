package engine

import (
	"testing"

	"github.com/anacrolix/torrent"
)

// TestToAnacrolixAllValues exercises every documented case of
// FilePriority.toAnacrolix, including the empty-string default
// and the "none" branch which the existing files_test only hits
// indirectly via SetFilePriority's normal/high paths.
func TestToAnacrolixAllValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   FilePriority
		want torrent.PiecePriority
	}{
		{FilePriorityNone, torrent.PiecePriorityNone},
		{FilePriorityNormal, torrent.PiecePriorityNormal},
		{"", torrent.PiecePriorityNormal}, // empty defaults to normal
		{FilePriorityHigh, torrent.PiecePriorityHigh},
	}
	for _, tc := range cases {
		got, err := tc.in.toAnacrolix()
		if err != nil {
			t.Errorf("toAnacrolix(%q) err = %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("toAnacrolix(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestPriorityLabelAllValues mirrors the above for the inverse
// helper. Covers every priorityLabel switch case plus the
// PiecePriorityNormal fallback. The high and normal cases were
// already covered by snapshotOf tests; the none case and the
// "internal priorities collapse to normal" fallback were not.
func TestPriorityLabelAllValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   torrent.PiecePriority
		want string
	}{
		{torrent.PiecePriorityNone, "none"},
		{torrent.PiecePriorityHigh, "high"},
		{torrent.PiecePriorityNormal, "normal"},
		{torrent.PiecePriorityReadahead, "normal"}, // internal → "normal"
		{torrent.PiecePriorityNext, "normal"},      // internal → "normal"
		{torrent.PiecePriorityNow, "normal"},       // internal → "normal"
	}
	for _, tc := range cases {
		if got := priorityLabel(tc.in); got != tc.want {
			t.Errorf("priorityLabel(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
