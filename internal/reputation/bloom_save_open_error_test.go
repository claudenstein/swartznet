package reputation_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/swartznet/swartznet/internal/reputation"
)

// TestBloomSaveOpenTempFails covers the tempfile-create error branch of
// BloomFilter.Save. Save now writes to a UNIQUE os.CreateTemp file (so
// concurrent saves never share a tmp inode), so the failure is forced by
// making the parent directory unwritable. The existing
// TestBloomSaveRenameFailure covers the rename branch.
//
// Skipped on Windows (directory permission semantics differ) and when
// running as root (mode bits are not enforced).
func TestBloomSaveOpenTempFails(t *testing.T) {
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
	path := filepath.Join(dir, "bloom.bin")

	bf, err := reputation.LoadOrCreateBloom(path)
	if err != nil {
		t.Fatalf("LoadOrCreateBloom: %v", err)
	}
	bf.Add([]byte("ubuntu-2404"))

	// Read+execute only, so os.CreateTemp cannot create the tempfile.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := bf.Save(); err == nil {
		t.Error("Save should fail when the tempfile cannot be created")
	}
}
