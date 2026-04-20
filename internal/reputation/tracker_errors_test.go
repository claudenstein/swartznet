package reputation_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/reputation"
)

func TestLoadOrCreateTrackerRejectsEmptyPath(t *testing.T) {
	t.Parallel()
	if _, err := reputation.LoadOrCreateTracker(""); err == nil {
		t.Error("LoadOrCreateTracker with empty path should error")
	}
}

// TestLoadOrCreateTrackerMalformedJSON covers the json.Unmarshal
// error branch: an existing file that isn't valid tracker JSON
// must return an error instead of silently starting fresh.
func TestLoadOrCreateTrackerMalformedJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "reputation.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := reputation.LoadOrCreateTracker(path); err == nil {
		t.Error("LoadOrCreateTracker on malformed JSON should error")
	}
}

// TestLoadOrCreateTrackerUnreadablePath covers the ReadFile error
// path for an error other than os.ErrNotExist. We point the path
// at a directory — os.ReadFile on a directory returns an EISDIR-
// style error that the function must propagate, not collapse to
// "fresh tracker."
func TestLoadOrCreateTrackerUnreadablePath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := reputation.LoadOrCreateTracker(dir); err == nil {
		t.Error("LoadOrCreateTracker on a directory path should error")
	}
}

// TestTrackerSaveRenameFailure covers the rename-failure branch of
// Save. Strategy: load a tracker bound to a fresh path so its
// internal .path is set, then replace that path on disk with a
// non-empty directory. Save writes the tempfile cleanly but the
// rename fails because os.Rename cannot replace a non-empty dir
// with a regular file.
func TestTrackerSaveRenameFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "reputation.json")

	// Bind the tracker to `path`. It does not exist yet, so Load
	// returns an empty tracker keyed on this path.
	tr, err := reputation.LoadOrCreateTracker(path)
	if err != nil {
		t.Fatalf("LoadOrCreateTracker: %v", err)
	}
	// Touch a record so MarshalIndent has something to serialise.
	tr.RecordReturned(pk(0xab), 3)

	// Now replace `path` with a non-empty directory. The next Save
	// will write `path.tmp` successfully but fail on Rename.
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "blocker"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := tr.Save(); err == nil {
		t.Error("Save should fail when the target path is a non-empty directory")
	}
	// The leftover tempfile must be cleaned up — Save defers an
	// os.Remove on its tmp on rename failure.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("tempfile leaked: stat err = %v", err)
	}
}
