package identity_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/swartznet/swartznet/internal/identity"
)

// TestLoadOrCreateWriteFileError covers the "os.WriteFile
// failed → wrapped 'identity: write' error" branch of
// LoadOrCreate. The trick: create the parent directory as 0o500
// (read+execute, no write) so MkdirAll on the existing path is a
// noop, loadFromDisk returns ENOENT (no key file yet), and the
// final WriteFile then fails for lack of write permission.
//
// Skipped on Windows because POSIX permission bits don't apply
// the same way; uid 0 also bypasses 0o500, so skip when running
// as root.
func TestLoadOrCreateWriteFileError(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits don't apply on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses 0o500 permission")
	}
	dir := t.TempDir()
	readonlyParent := filepath.Join(dir, "lockedparent")
	if err := os.Mkdir(readonlyParent, 0o500); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// Restore writable bits so t.TempDir cleanup can recurse in.
	t.Cleanup(func() { _ = os.Chmod(readonlyParent, 0o700) })

	path := filepath.Join(readonlyParent, "key")
	if _, err := identity.LoadOrCreate(path); err == nil {
		t.Error("LoadOrCreate should fail when parent is read-only and key file is absent")
	}
}
