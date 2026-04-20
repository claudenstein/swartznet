package companion_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/companion"
)

// TestWriteCompanionFilesEmptyDir covers the dir == "" guard.
func TestWriteCompanionFilesEmptyDir(t *testing.T) {
	t.Parallel()
	_, _, err := companion.WriteCompanionFiles("", companion.CompanionIndex{})
	if err == nil {
		t.Error("expected error for empty dir")
	}
	if !strings.Contains(err.Error(), "empty dir") {
		t.Errorf("err = %q, want it to mention 'empty dir'", err.Error())
	}
}

// TestWriteCompanionFilesMkdirFails covers the MkdirAll error
// branch — point dir at a path inside a regular file so MkdirAll
// fails with ENOTDIR.
func TestWriteCompanionFilesMkdirFails(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("path-into-file semantics differ on Windows")
	}
	root := t.TempDir()
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(blocker, "child")

	_, _, err := companion.WriteCompanionFiles(bad, companion.CompanionIndex{})
	if err == nil {
		t.Error("expected MkdirAll error when dir lives under a regular file")
	}
	if !strings.Contains(err.Error(), "mkdir") {
		t.Errorf("err = %q, want it to mention 'mkdir'", err.Error())
	}
}

// TestWriteCompanionFilesAtomicWritePayloadFails covers the
// payload-write error branch — pre-plant a non-empty directory at
// `<dir>/<FormatFileName>.tmp` so atomicWrite fails when it tries
// to truncate-open the tempfile.
func TestWriteCompanionFilesAtomicWritePayloadFails(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("opening a directory for writing has different semantics on Windows")
	}
	dir := t.TempDir()
	tmp := filepath.Join(dir, companion.FormatFileName+".tmp")
	if err := os.MkdirAll(filepath.Join(tmp, "child"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, _, err := companion.WriteCompanionFiles(dir, companion.CompanionIndex{})
	if err == nil {
		t.Error("expected payload-write error")
	}
	if !strings.Contains(err.Error(), "write payload") {
		t.Errorf("err = %q, want it to mention 'write payload'", err.Error())
	}
}
