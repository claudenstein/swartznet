package engine_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/swartznet/swartznet/internal/engine"
)

// TestCreateTorrentFileWriteTempFails covers the os.WriteFile
// error branch of CreateTorrentFile — plant a non-empty
// directory at `<outPath>.tmp` so the truncate-open in
// os.WriteFile fails. The existing rename and sign error tests
// cover the other branches; this fills in the WriteFile branch.
//
// Skipped on Windows because of differing semantics around
// opening a directory for writing.
func TestCreateTorrentFileWriteTempFails(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("opening a directory for writing has different semantics on Windows")
	}
	eng := newTestEngine(t)

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "x.bin")
	if err := os.WriteFile(srcPath, []byte(fillTo(32*1024)), 0o644); err != nil {
		t.Fatal(err)
	}

	outPath := filepath.Join(dir, "x.torrent")
	tmp := outPath + ".tmp"
	// Plant a NON-empty directory at the tempfile path so
	// os.WriteFile fails when it tries to truncate-open it.
	if err := os.MkdirAll(filepath.Join(tmp, "child"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, _, err := eng.CreateTorrentFile(engine.CreateTorrentOptions{Root: srcPath}, outPath); err == nil {
		t.Error("CreateTorrentFile should fail when tempfile path is a non-empty directory")
	}
	// Planted dir must still exist (we didn't accidentally rmdir it).
	if st, err := os.Stat(tmp); err != nil || !st.IsDir() {
		t.Errorf("planted tmp dir disappeared: stat=%v err=%v", st, err)
	}
}
