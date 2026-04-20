package companion

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestAtomicWriteOpenError covers the OpenFile-failed branch of
// atomicWrite — pointing at a tempfile path inside a directory
// that does not exist returns an error from OpenFile.
func TestAtomicWriteOpenError(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "no-such-dir", "out.bin")
	err := atomicWrite(missing, []byte("hello"))
	if err == nil {
		t.Errorf("atomicWrite to missing dir should error")
	}
}

// TestAtomicWriteRenameError covers the os.Rename-failed branch
// of atomicWrite — plant a non-empty directory at the target path
// so Rename refuses to overwrite it.
//
// Skipped on Windows where rename-onto-non-empty-dir semantics
// differ; on Linux/macOS the kernel returns ENOTEMPTY.
func TestAtomicWriteRenameError(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("rename-onto-dir semantics differ on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "victim")
	// Plant a non-empty directory at the target.
	if err := os.MkdirAll(filepath.Join(target, "child"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	err := atomicWrite(target, []byte("payload"))
	if err == nil {
		t.Errorf("atomicWrite over non-empty dir should error")
	}
	// Tempfile must NOT have been left behind on the failure path.
	if _, statErr := os.Stat(target + ".tmp"); !os.IsNotExist(statErr) {
		t.Errorf("tempfile %q should have been cleaned up after rename failure (statErr=%v)",
			target+".tmp", statErr)
	}
}

// TestAtomicWriteSuccess covers the happy path — file does not
// exist, the write completes, and the bytes can be read back.
func TestAtomicWriteSuccess(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "out.bin")
	want := []byte("payload-bytes")
	if err := atomicWrite(target, want); err != nil {
		t.Fatalf("atomicWrite: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("read = %q, want %q", got, want)
	}
}
