package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// writeMinimalTorrentFile produces the smallest valid single-file
// torrent we can — one zero-length file and one zero-length piece —
// and writes it to path. Returns the 40-char hex infohash. Mirrors
// the helper in internal/engine/session_test.go but lives here so
// the daemon-package internal tests don't pull in a cross-package
// helper.
func writeMinimalTorrentFile(t *testing.T, path string) string {
	t.Helper()
	info := metainfo.Info{
		Name:        "controller-adapter-fixture",
		Length:      0,
		PieceLength: 16384,
		Pieces:      []byte{},
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	mi := metainfo.MetaInfo{
		InfoBytes: infoBytes,
		CreatedBy: "torrent_files_test",
	}
	miBytes, err := bencode.Marshal(mi)
	if err != nil {
		t.Fatalf("marshal mi: %v", err)
	}
	if err := os.WriteFile(path, miBytes, 0o644); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	return metainfo.HashBytes(infoBytes).HexString()
}

// TestControllerAdapterTorrentFilesAndSnapshotsHappyPath drives a
// real torrent through AddTorrentFile and verifies the adapter
// translates engine.FileSnapshot → httpapi.TorrentFile and
// engine.TorrentSnapshot → httpapi.TorrentSnapshot for a populated
// engine (the previously-uncovered success branches).
func TestControllerAdapterTorrentFilesAndSnapshotsHappyPath(t *testing.T) {
	t.Parallel()
	eng, cleanup := newAdapterEngine(t)
	defer cleanup()

	torrentPath := filepath.Join(t.TempDir(), "fixture.torrent")
	ih := writeMinimalTorrentFile(t, torrentPath)
	if _, err := eng.AddTorrentFile(torrentPath); err != nil {
		t.Fatalf("AddTorrentFile: %v", err)
	}

	c := &controllerAdapter{eng: eng}

	files, err := c.TorrentFiles(ih)
	if err != nil {
		t.Fatalf("TorrentFiles: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1", len(files))
	}
	got := files[0]
	if got.Index != 0 {
		t.Errorf("Index = %d, want 0", got.Index)
	}
	if got.Length != 0 {
		t.Errorf("Length = %d, want 0", got.Length)
	}
	// DisplayPath is set from the file's display path; for our
	// single-file torrent it should equal the torrent name.
	if got.DisplayPath == "" {
		t.Error("DisplayPath should not be empty")
	}

	snaps := c.TorrentSnapshots()
	if len(snaps) != 1 {
		t.Fatalf("snapshots len = %d, want 1", len(snaps))
	}
	if snaps[0].InfoHash != ih {
		t.Errorf("snapshot InfoHash = %q, want %q", snaps[0].InfoHash, ih)
	}
}
