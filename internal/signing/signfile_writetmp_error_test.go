package signing_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/swartznet/swartznet/internal/signing"
)

// TestSignFileWriteTempFails covers SignFile's WriteFile-error
// branch by planting a non-empty directory at the path SignFile
// will use for its tempfile (`<path>.tmp`). os.WriteFile then
// errors when it tries to truncate-open a directory, and the
// wrapped "signing: write tmp" error must propagate.
//
// Skipped on Windows because of differing semantics around
// opening a directory for writing.
func TestSignFileWriteTempFails(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("opening a directory for writing has different semantics on Windows")
	}
	_, priv := newKey(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "input.torrent")
	if err := os.WriteFile(path, miniTorrent(t), 0o644); err != nil {
		t.Fatal(err)
	}
	// Plant a NON-empty directory at the tempfile path so the
	// truncate-open in SignFile fails.
	tmp := path + ".tmp"
	if err := os.MkdirAll(filepath.Join(tmp, "child"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := signing.SignFile(path, priv); err == nil {
		t.Error("SignFile should error when tempfile path is a non-empty directory")
	}
	// And the planted dir must still be there afterwards (we
	// didn't accidentally rmdir or otherwise stomp it).
	if st, err := os.Stat(tmp); err != nil || !st.IsDir() {
		t.Errorf("planted tmp dir disappeared: stat=%v err=%v", st, err)
	}
}
