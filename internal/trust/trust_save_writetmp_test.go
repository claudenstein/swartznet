package trust_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/trust"
)

// TestSaveWriteTempFails covers the tempfile-create error branch of
// save. save() now writes to a UNIQUE os.CreateTemp file (so concurrent
// writers never share a tmp inode), so the failure is forced by making
// the parent directory unwritable rather than by planting a directory at
// a fixed tmp path.
//
// Skipped on Windows (directory permission semantics differ) and when
// running as root (mode bits are not enforced).
func TestSaveWriteTempFails(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("directory permission semantics differ on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory write permissions")
	}
	parent := t.TempDir()
	dir := filepath.Join(parent, "share")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "trust.json")

	s, err := trust.LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}

	// Make the parent dir read+execute only so os.CreateTemp cannot create
	// the tempfile. Restore write perms afterwards so t.TempDir cleanup can
	// remove the tree.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	good := strings.Repeat("ab", 32)
	if err := s.Add(good, "first"); err == nil {
		t.Error("Add should fail when save's tempfile cannot be created")
	}
}
