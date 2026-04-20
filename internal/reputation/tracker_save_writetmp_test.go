package reputation_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/swartznet/swartznet/internal/reputation"
)

// TestTrackerSaveWriteTempFails covers the os.WriteFile-error
// branch of Tracker.Save — plant a non-empty directory at
// `<path>.tmp` so the truncate-open in os.WriteFile fails. The
// existing TestTrackerSaveRenameFailure covers the rename branch.
//
// Skipped on Windows because of differing semantics around
// opening a directory for writing.
func TestTrackerSaveWriteTempFails(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("opening a directory for writing has different semantics on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "tracker.json")

	tr, err := reputation.LoadOrCreateTracker(path)
	if err != nil {
		t.Fatalf("LoadOrCreateTracker: %v", err)
	}
	tr.RecordReturned(reputation.PubKeyHex("aa"), 1)

	// Plant a NON-empty directory at the tempfile path.
	tmp := path + ".tmp"
	if err := os.MkdirAll(filepath.Join(tmp, "child"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := tr.Save(); err == nil {
		t.Error("Save should fail when tempfile path is a non-empty directory")
	}
	// Planted dir must still exist.
	if st, err := os.Stat(tmp); err != nil || !st.IsDir() {
		t.Errorf("planted tmp dir disappeared: stat=%v err=%v", st, err)
	}
}
