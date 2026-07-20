package reputation_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/swartznet/swartznet/internal/reputation"
)

// TestTrackerSaveWriteTempFails covers the tempfile-create error branch of
// Tracker.Save. Save now writes to a UNIQUE os.CreateTemp file (so
// concurrent saves never share a tmp inode), so the failure is forced by
// making the parent directory unwritable. The existing
// TestTrackerSaveRenameFailure covers the rename branch.
//
// Skipped on Windows (directory permission semantics differ) and when
// running as root (mode bits are not enforced).
func TestTrackerSaveWriteTempFails(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("directory permission semantics differ on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory write permissions")
	}
	parent := t.TempDir()
	dir := filepath.Join(parent, "d")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "tracker.json")

	tr, err := reputation.LoadOrCreateTracker(path)
	if err != nil {
		t.Fatalf("LoadOrCreateTracker: %v", err)
	}
	tr.RecordReturned(reputation.PubKeyHex("aa"), 1)

	// Read+execute only, so os.CreateTemp cannot create the tempfile.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := tr.Save(); err == nil {
		t.Error("Save should fail when the tempfile cannot be created")
	}
}
