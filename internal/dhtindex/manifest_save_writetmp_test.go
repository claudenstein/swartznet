package dhtindex_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// TestManifestSaveWriteTempFails covers the os.WriteFile error
// branch of Manifest.Save — plant a non-empty directory at
// `<path>.tmp` so the truncate-open in os.WriteFile fails. The
// existing TestManifestSaveRenameFailure covers the rename
// branch; this one fills in the WriteFile branch.
//
// Skipped on Windows because of differing semantics around
// opening a directory for writing.
func TestManifestSaveWriteTempFails(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("opening a directory for writing has different semantics on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")

	mf, err := dhtindex.LoadOrCreateManifest(path)
	if err != nil {
		t.Fatalf("LoadOrCreateManifest: %v", err)
	}
	if _, err := mf.AddHit("ubuntu", dhtindex.KeywordHit{IH: ihBytes(2), N: "x"}); err != nil {
		t.Fatal(err)
	}

	// Plant a NON-empty directory at the tempfile path so
	// os.WriteFile fails when it tries to truncate-open it.
	tmp := path + ".tmp"
	if err := os.MkdirAll(filepath.Join(tmp, "child"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := mf.Save(); err == nil {
		t.Error("Save should fail when tempfile path is a non-empty directory")
	}
	// Planted dir must still exist (we didn't accidentally rmdir it).
	if st, err := os.Stat(tmp); err != nil || !st.IsDir() {
		t.Errorf("planted tmp dir disappeared: stat=%v err=%v", st, err)
	}
}
