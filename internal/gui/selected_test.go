package gui

import (
	"testing"

	"github.com/swartznet/swartznet/internal/engine"
)

// TestSelectedInfoHashEdgeCases covers selectedInfoHash's two
// out-of-range guards (selected < 0 and selected >= len(snaps))
// plus the in-range happy path.
func TestSelectedInfoHashEdgeCases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		snaps    []engine.TorrentSnapshot
		selected int
		want     string
	}{
		{
			name:     "selected -1 (no row)",
			snaps:    []engine.TorrentSnapshot{{InfoHash: "aaa"}},
			selected: -1,
			want:     "",
		},
		{
			name:     "selected past end",
			snaps:    []engine.TorrentSnapshot{{InfoHash: "aaa"}},
			selected: 5,
			want:     "",
		},
		{
			name:     "selected 0",
			snaps:    []engine.TorrentSnapshot{{InfoHash: "aaa"}, {InfoHash: "bbb"}},
			selected: 0,
			want:     "aaa",
		},
		{
			name:     "selected 1",
			snaps:    []engine.TorrentSnapshot{{InfoHash: "aaa"}, {InfoHash: "bbb"}},
			selected: 1,
			want:     "bbb",
		},
		{
			name:     "empty snaps",
			snaps:    nil,
			selected: 0,
			want:     "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dl := &downloadsTab{snaps: c.snaps, selected: c.selected}
			if got := dl.selectedInfoHash(); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}
