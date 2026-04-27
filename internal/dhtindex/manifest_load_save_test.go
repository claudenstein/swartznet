package dhtindex_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// TestLoadOrCreateManifestNullEntriesField covers the
// `if m.Entries == nil { m.Entries = make(...) }` defensive arm in
// LoadOrCreateManifest. A manifest that decodes with
// json.Unmarshal but whose "entries" field is null leaves the
// in-memory map nil; the load path must rebuild the map so
// subsequent AddHit doesn't panic on a nil-map assignment.
func TestLoadOrCreateManifestNullEntriesField(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(path, []byte(`{"entries":null}`), 0o600); err != nil {
		t.Fatal(err)
	}
	mf, err := dhtindex.LoadOrCreateManifest(path)
	if err != nil {
		t.Fatalf("LoadOrCreateManifest: %v", err)
	}
	// Empty Entries → AddHit must succeed (proves the map was rebuilt).
	if _, err := mf.AddHit("k", dhtindex.KeywordHit{IH: ihBytes(1), N: "x"}); err != nil {
		t.Errorf("AddHit on null-entries manifest: %v", err)
	}
}

// TestManifestSaveTmpWriteFailure covers Save's
// `os.WriteFile(tmp, ...)` error arm. Strip write permission on
// the manifest's parent directory after load so the .tmp file
// cannot be created.
func TestManifestSaveTmpWriteFailure(t *testing.T) {
	t.Parallel()
	if os.Getuid() == 0 {
		t.Skip("running as root, chmod 0 doesn't deny writes")
	}
	dir := t.TempDir()
	subDir := filepath.Join(dir, "manifests")
	if err := os.Mkdir(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(subDir, "manifest.json")
	mf, err := dhtindex.LoadOrCreateManifest(path)
	if err != nil {
		t.Fatalf("LoadOrCreateManifest: %v", err)
	}
	if _, err := mf.AddHit("k", dhtindex.KeywordHit{IH: ihBytes(1), N: "x"}); err != nil {
		t.Fatal(err)
	}

	// Strip write+execute on the parent dir so WriteFile on tmp fails.
	if err := os.Chmod(subDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(subDir, 0o755) })

	if err := mf.Save(); err == nil {
		t.Error("Save should fail when tmp WriteFile is denied")
	}
}
