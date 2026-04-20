package dhtindex_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// TestLoadOrCreateManifestMkdirFailure covers the MkdirAll error
// branch. The manifest path's parent is a regular file, so
// MkdirAll cannot create the dir and the load returns the wrapped
// error.
func TestLoadOrCreateManifestMkdirFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Manifest path lives under blocker/ — MkdirAll on blocker
	// (which is a regular file) fails.
	if _, err := dhtindex.LoadOrCreateManifest(filepath.Join(blocker, "manifest.json")); err == nil {
		t.Error("LoadOrCreateManifest should fail when parent dir cannot be created")
	}
}

// TestLoadOrCreateManifestUnreadable covers the non-NotExist
// ReadFile error: pointing at a directory makes os.ReadFile fail
// with a non-ErrNotExist error which the function must propagate.
func TestLoadOrCreateManifestUnreadable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := dhtindex.LoadOrCreateManifest(dir); err == nil {
		t.Error("LoadOrCreateManifest on a directory should error (non-NotExist ReadFile failure)")
	}
}

// TestLoadOrCreateManifestMalformedJSON covers the
// json.Unmarshal error branch: an existing file that isn't valid
// manifest JSON returns an error instead of silently starting
// fresh.
func TestLoadOrCreateManifestMalformedJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := dhtindex.LoadOrCreateManifest(path); err == nil {
		t.Error("LoadOrCreateManifest on malformed JSON should error")
	}
}

// TestManifestSaveRenameFailure covers the rename-failure branch.
// We bind the manifest to a path, then plant a non-empty directory
// at that path so os.Rename can't replace it with the regular
// tempfile. Save must surface the wrapped error and clean up the
// leftover *.tmp.
func TestManifestSaveRenameFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")

	mf, err := dhtindex.LoadOrCreateManifest(path)
	if err != nil {
		t.Fatalf("LoadOrCreateManifest: %v", err)
	}
	if _, err := mf.AddHit("ubuntu", dhtindex.KeywordHit{IH: ihBytes(1), N: "x"}); err != nil {
		t.Fatal(err)
	}

	// Replace the path with a non-empty directory so rename fails.
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "blocker"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := mf.Save(); err == nil {
		t.Error("Save should fail when the target path is a non-empty directory")
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("tempfile leaked: stat err = %v", err)
	}
}
