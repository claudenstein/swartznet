package reputation_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/swartznet/swartznet/internal/reputation"
)

// TestBloomSaveOpenTempFails covers the os.OpenFile error
// branch of BloomFilter.Save — plant a non-empty directory at
// `<path>.tmp` so the truncate-open in os.OpenFile fails. The
// existing TestBloomSaveRenameFailure covers the rename branch;
// this one fills in the OpenFile branch.
//
// Skipped on Windows because of differing semantics around
// opening a directory for writing.
func TestBloomSaveOpenTempFails(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("opening a directory for writing has different semantics on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "bloom.bin")

	bf, err := reputation.LoadOrCreateBloom(path)
	if err != nil {
		t.Fatalf("LoadOrCreateBloom: %v", err)
	}
	bf.Add([]byte("ubuntu-2404"))

	// Plant a NON-empty directory at the tempfile path so the
	// truncate-open in os.OpenFile fails.
	tmp := path + ".tmp"
	if err := os.MkdirAll(filepath.Join(tmp, "child"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := bf.Save(); err == nil {
		t.Error("Save should fail when tempfile path is a non-empty directory")
	}
	// Planted dir must still exist.
	if st, err := os.Stat(tmp); err != nil || !st.IsDir() {
		t.Errorf("planted tmp dir disappeared: stat=%v err=%v", st, err)
	}
}
