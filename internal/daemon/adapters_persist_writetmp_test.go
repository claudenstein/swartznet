package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestPersistFollowsWriteTmpFails covers persistFollows's
// os.WriteFile error branch — plant a non-empty directory at
// `<followPath>.tmp` so the truncate-open in os.WriteFile fails.
// The existing TestCompanionAdapterPersistRenameFailure covers
// the os.Rename branch; this fills in the WriteFile branch.
//
// Skipped on Windows because of differing semantics around
// opening a directory for writing.
func TestPersistFollowsWriteTmpFails(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("opening a directory for writing has different semantics on Windows")
	}
	w := newAdapterSubscriberWorker(t)
	dir := t.TempDir()
	followPath := filepath.Join(dir, "follows.json")
	// Plant a NON-empty directory at the tempfile path so
	// os.WriteFile fails when it tries to truncate-open it.
	tmp := followPath + ".tmp"
	if err := os.MkdirAll(filepath.Join(tmp, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := newCompanionAdapter(nil, w, followPath)

	var pk [32]byte
	pk[0] = 0xAB
	if err := a.Follow(pk, "wt-fail"); err == nil {
		t.Error("Follow should error when WriteFile to tempfile fails")
	}
	// Planted dir must still exist (we didn't accidentally rmdir it).
	if st, err := os.Stat(tmp); err != nil || !st.IsDir() {
		t.Errorf("planted tmp dir disappeared: stat=%v err=%v", st, err)
	}
}
