package gui

import (
	"testing"

	"github.com/swartznet/swartznet/internal/engine"
)

// TestSelectedInfoHashEdgeCases covers selectedInfoHash's guards
// (no selection, selected torrent no longer present) plus the
// in-range happy path. The primary selection is keyed by infohash
// so it survives re-sorts; a key that no longer resolves against
// dl.snaps must yield "" rather than some other torrent.
func TestSelectedInfoHashEdgeCases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		snaps       []engine.TorrentSnapshot
		selectedKey string
		want        string
	}{
		{
			name:        "no selection",
			snaps:       []engine.TorrentSnapshot{{InfoHash: "aaa"}},
			selectedKey: "",
			want:        "",
		},
		{
			name:        "selected torrent gone",
			snaps:       []engine.TorrentSnapshot{{InfoHash: "aaa"}},
			selectedKey: "zzz",
			want:        "",
		},
		{
			name:        "first torrent selected",
			snaps:       []engine.TorrentSnapshot{{InfoHash: "aaa"}, {InfoHash: "bbb"}},
			selectedKey: "aaa",
			want:        "aaa",
		},
		{
			name:        "second torrent selected",
			snaps:       []engine.TorrentSnapshot{{InfoHash: "aaa"}, {InfoHash: "bbb"}},
			selectedKey: "bbb",
			want:        "bbb",
		},
		{
			name:        "empty snaps",
			snaps:       nil,
			selectedKey: "aaa",
			want:        "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dl := &downloadsTab{snaps: c.snaps, selectedKey: c.selectedKey}
			if got := dl.selectedInfoHash(); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// TestSelectedInfoHashSurvivesResort is the regression test for the
// stale-selection bug: dl.selected used to be a row INDEX into
// dl.snaps, but pollLoop re-sorts snaps every 2 s — so an action
// issued after an intervening re-sort hit whatever torrent had
// slid into that row (Remove being destructive made this nasty).
// With the infohash-keyed primary selection the same torrent must
// resolve before and after the re-sort.
func TestSelectedInfoHashSurvivesResort(t *testing.T) {
	t.Parallel()
	dl := &downloadsTab{
		snaps: []engine.TorrentSnapshot{
			{InfoHash: "bbb", Name: "beta"},
			{InfoHash: "aaa", Name: "alpha"},
		},
		sortCol: -1,
	}

	// User clicks row 0 → torrent "bbb" becomes the selection.
	dl.selectedKey = dl.snaps[0].InfoHash
	if got := dl.selectedInfoHash(); got != "bbb" {
		t.Fatalf("before re-sort: got %q, want bbb", got)
	}

	// Background poll re-sorts by Name ascending; "aaa" now sits
	// in row 0 where "bbb" used to be.
	dl.mu.Lock()
	dl.sortCol = 0
	dl.sortSnapsLocked()
	dl.mu.Unlock()
	if dl.snaps[0].InfoHash != "aaa" {
		t.Fatalf("re-sort did not move rows: snaps[0]=%q", dl.snaps[0].InfoHash)
	}

	// The selection must still resolve to "bbb" — the old index-
	// based model would have returned "aaa" here.
	if got := dl.selectedInfoHash(); got != "bbb" {
		t.Fatalf("after re-sort: got %q, want bbb (stale-index regression)", got)
	}
	if targets := dl.actionTargets(); len(targets) != 1 || targets[0] != "bbb" {
		t.Fatalf("actionTargets after re-sort: got %v, want [bbb]", targets)
	}
}
