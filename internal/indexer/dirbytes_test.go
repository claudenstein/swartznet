package indexer

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDirBytesReadDirErrorPropagates covers the os.ReadDir
// error branch of dirBytes — pointing at a non-existent path
// returns the wrapped error and a zero total.
func TestDirBytesReadDirErrorPropagates(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "no-such-dir")
	total, err := dirBytes(missing)
	if err == nil {
		t.Errorf("expected error for missing dir, got total=%d", total)
	}
	if total != 0 {
		t.Errorf("total = %d, want 0 on error", total)
	}
}

// TestDirBytesSumsFilesAndRecurses covers the happy path
// (regular file, recursion into subdirectory) plus the
// "skip subdir errors silently" contract — the inner dirBytes
// call discards its error so a permission-denied subdir doesn't
// abort the outer walk.
func TestDirBytesSumsFilesAndRecurses(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.bin"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "b.bin"), []byte("123"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := dirBytes(root)
	if err != nil {
		t.Fatalf("dirBytes: %v", err)
	}
	if got != 8 {
		t.Errorf("total = %d, want 8 (5 + 3)", got)
	}
}

// TestDirBytesEmptyDir returns 0 with no error for an
// empty-but-existing dir.
func TestDirBytesEmptyDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	got, err := dirBytes(dir)
	if err != nil {
		t.Fatalf("dirBytes: %v", err)
	}
	if got != 0 {
		t.Errorf("total = %d, want 0 for empty dir", got)
	}
}
