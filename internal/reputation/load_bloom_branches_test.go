package reputation_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/reputation"
)

// TestLoadOrCreateBloomMkdirFails covers the MkdirAll-error
// branch — plant a regular file at the path's would-be parent
// so MkdirAll fails with ENOTDIR.
func TestLoadOrCreateBloomMkdirFails(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("path-into-file semantics differ on Windows")
	}
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(blocker, "bloom.bin")
	_, err := reputation.LoadOrCreateBloom(bad)
	if err == nil {
		t.Error("LoadOrCreateBloom should fail when parent dir cannot be created")
	}
	if !strings.Contains(err.Error(), "mkdir bloom dir") {
		t.Errorf("err = %q, want it to wrap 'mkdir bloom dir'", err.Error())
	}
}

// TestLoadOrCreateBloomOpenNonENOENTErrors covers the
// non-ErrNotExist os.Open error branch — point at a path whose
// parent is a regular file inside an existing dir, so MkdirAll
// succeeds (parent is "blocker-with-perms" already there as a
// dir), but os.Open returns ENOTDIR or similar non-ErrNotExist
// error. We use a 0o000-perm parent to trip Open with EACCES.
func TestLoadOrCreateBloomOpenNonENOENTErrors(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits don't apply on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses 0o000 permission")
	}
	dir := t.TempDir()
	parent := filepath.Join(dir, "lockedparent")
	// Pre-create + populate so MkdirAll is a noop.
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	bloomPath := filepath.Join(parent, "bloom.bin")
	if err := os.WriteFile(bloomPath, []byte("not bloom"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Now lock the parent — MkdirAll is a noop (it exists), but
	// os.Open(bloomPath) needs read+execute on the parent.
	if err := os.Chmod(parent, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	_, err := reputation.LoadOrCreateBloom(bloomPath)
	if err == nil {
		t.Error("LoadOrCreateBloom should error when the parent dir is unreadable")
	}
	if !strings.Contains(err.Error(), "open bloom") {
		t.Errorf("err = %q, want it to wrap 'open bloom'", err.Error())
	}
}
