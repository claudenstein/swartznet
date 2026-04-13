package engine_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/engine"
)

// waitForAllPriorities polls up to timeout for every file in the
// torrent to report the given priority. Needed because
// autoDownload loops over files one at a time, so reading mid-
// loop can see a mixed state.
func waitForAllPriorities(t *testing.T, eng *engine.Engine, ihHex string, want string, timeout time.Duration) []engine.FileSnapshot {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		files, err := eng.TorrentFiles(ihHex)
		if err != nil {
			t.Fatalf("TorrentFiles: %v", err)
		}
		allMatch := len(files) > 0
		for _, f := range files {
			if f.Priority != want {
				allMatch = false
				break
			}
		}
		if allMatch {
			return files
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for all files priority=%q (last snapshot: %+v)", want, files)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestTorrentFilesAndSetPriority builds a multi-file torrent,
// adds it via AddTorrentMetaInfo, and verifies the file list +
// priority changes round-trip through TorrentFiles.
func TestTorrentFilesAndSetPriority(t *testing.T) {
	t.Parallel()

	eng := newTestEngine(t)

	// Build a 3-file torrent on disk, then load it into the engine.
	dir := t.TempDir()
	root := filepath.Join(dir, "content")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, name := range []string{"a.bin", "b.bin", "c.bin"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(fillTo(32*1024)), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	mi, err := eng.CreateTorrent(engine.CreateTorrentOptions{Root: root})
	if err != nil {
		t.Fatalf("CreateTorrent: %v", err)
	}
	if _, err := eng.AddTorrentMetaInfo(mi); err != nil {
		t.Fatalf("AddTorrentMetaInfo: %v", err)
	}
	ihHex := mi.HashInfoBytes().HexString()

	// Initial snapshot: wait for autoDownload goroutine to flip
	// every file to normal priority. autoDownload iterates files
	// and calls SetPriority one at a time; reading back mid-loop
	// can see a mixed state, so poll until every file is normal
	// (or timeout). Up to 5 seconds is plenty — the goroutine
	// just waits on GotInfo which has already fired.
	files := waitForAllPriorities(t, eng, ihHex, "normal", 5*time.Second)
	if len(files) != 3 {
		t.Fatalf("file count: got %d, want 3", len(files))
	}
	for i, f := range files {
		if f.Index != i {
			t.Errorf("file %d index: got %d", i, f.Index)
		}
		if f.Priority != "normal" {
			t.Errorf("file %d priority: got %q, want normal", i, f.Priority)
		}
		if f.Length != 32*1024 {
			t.Errorf("file %d length: got %d, want 32 KiB", i, f.Length)
		}
	}

	// Flip middle file to "none".
	if err := eng.SetFilePriority(ihHex, 1, engine.FilePriorityNone); err != nil {
		t.Fatalf("SetFilePriority none: %v", err)
	}
	files, _ = eng.TorrentFiles(ihHex)
	if files[1].Priority != "none" {
		t.Errorf("after SetFilePriority(none): got %q", files[1].Priority)
	}

	// Flip first file to "high".
	if err := eng.SetFilePriority(ihHex, 0, engine.FilePriorityHigh); err != nil {
		t.Fatalf("SetFilePriority high: %v", err)
	}
	files, _ = eng.TorrentFiles(ihHex)
	if files[0].Priority != "high" {
		t.Errorf("after SetFilePriority(high): got %q", files[0].Priority)
	}

	// Out-of-range rejected.
	if err := eng.SetFilePriority(ihHex, 99, engine.FilePriorityNormal); err == nil {
		t.Error("expected error for out-of-range index")
	}

	// Invalid priority rejected.
	if err := eng.SetFilePriority(ihHex, 0, "turbo"); err == nil {
		t.Error("expected error for invalid priority")
	}
}

// TestTorrentFilesUnknownInfohash rejects bad infohash.
func TestTorrentFilesUnknownInfohash(t *testing.T) {
	t.Parallel()

	eng := newTestEngine(t)

	if _, err := eng.TorrentFiles("0000000000000000000000000000000000000000"); err == nil {
		t.Error("expected error for unknown infohash")
	}
}
