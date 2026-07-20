package gui

import (
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/engine"
)

// TestDownloadsSelectionSurvivesReorder pins the round-8 HIGH fix: the Downloads
// tab resolves pause/resume/remove by the INFOHASH captured at selection time,
// not by a row index into the per-poll-rebuilt snapshot list. Before the fix, a
// poll that reordered the list (Torrents() ranges a map) left the index pointing
// at a DIFFERENT torrent, so Remove dropped one the user never selected.
func TestDownloadsSelectionSurvivesReorder(t *testing.T) {
	dl := &downloadsTab{selected: -1}
	a, b, c := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	dl.snaps = []engine.TorrentSnapshot{{InfoHash: a}, {InfoHash: b}, {InfoHash: c}}

	dl.selectRow(1) // user clicks the row showing torrent b
	if got := dl.selectedInfoHash(); got != b {
		t.Fatalf("captured %q at selection, want %q", got, b)
	}

	// A poll reorders the snapshot (map-iteration randomness): index 1 is now `a`.
	dl.snaps = []engine.TorrentSnapshot{{InfoHash: c}, {InfoHash: a}, {InfoHash: b}}

	if got := dl.selectedInfoHash(); got != b {
		t.Errorf("selection drifted to %q after a reorder — the action would hit the wrong torrent (want %q)", got, b)
	}
}
