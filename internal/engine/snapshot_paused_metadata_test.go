package engine_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/engine"
)

// TestSnapshotPausedTorrentWithMetadata covers snapshotOf's
// post-metadata `case paused: status = "paused"` arm. The
// existing pause tests use magnet adds (no metadata), so the
// pre-metadata branch covers paused; the post-metadata branch
// stays cold. AddTorrentMetaInfo adds with metadata in hand,
// so a Pause + TorrentSnapshots call drives the post-metadata
// switch.
func TestSnapshotPausedTorrentWithMetadata(t *testing.T) {
	t.Parallel()
	eng := newTestEngine(t)

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.bin")
	if err := os.WriteFile(srcPath, []byte(fillTo(32*1024)), 0o644); err != nil {
		t.Fatal(err)
	}
	mi, err := eng.CreateTorrent(engine.CreateTorrentOptions{Root: srcPath})
	if err != nil {
		t.Fatal(err)
	}
	h, err := eng.AddTorrentMetaInfo(mi)
	if err != nil {
		t.Fatal(err)
	}
	_ = h
	ihHex := mi.HashInfoBytes().HexString()
	if err := eng.PauseTorrent(ihHex); err != nil {
		t.Fatalf("PauseTorrent: %v", err)
	}

	var got string
	for _, s := range eng.TorrentSnapshots() {
		if s.InfoHash == ihHex {
			got = s.Status
		}
	}
	if got != "paused" {
		t.Errorf("status = %q, want %q (post-metadata paused arm)", got, "paused")
	}
}
