package companion_test

import (
	"testing"

	"github.com/swartznet/swartznet/internal/companion"
)

// TestBuildFromIndexRespectsMaxFilesPerTorrent covers the
// MaxFilesPerTorrent cap in collectFileRecords. Seed an index
// with a torrent that has 3 file paths, then ask BuildFromIndex
// to cap at 2 — the resulting TorrentRecord must have only the
// first 2 files.
func TestBuildFromIndexRespectsMaxFilesPerTorrent(t *testing.T) {
	t.Parallel()
	idx := seedIndex(t) // ubuntu has 2 files; debian has 1
	opts := companion.DefaultBuildOptions()
	opts.IncludeContent = true
	opts.MaxFilesPerTorrent = 1

	got, err := companion.BuildFromIndex(idx, "abc", opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range got.Torrents {
		if len(tr.Files) > 1 {
			t.Errorf("torrent %s has %d files, want at most 1 (MaxFilesPerTorrent=1)",
				tr.InfoHash, len(tr.Files))
		}
	}
}
