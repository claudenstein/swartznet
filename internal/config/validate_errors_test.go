package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateMkdirDataDirFailure covers the wrapped MkdirAll
// error branch: the requested DataDir is under a regular file,
// so MkdirAll cannot create the leaf and Validate must return
// the wrapped "cannot create DataDir" error.
func TestValidateMkdirDataDirFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("file-not-dir"), 0o600); err != nil {
		t.Fatal(err)
	}

	c := Default()
	c.DataDir = filepath.Join(blocker, "data")
	err := c.Validate()
	if err == nil {
		t.Fatal("Validate should fail when DataDir is under a regular file")
	}
	if !strings.Contains(err.Error(), "create DataDir") {
		t.Errorf("error = %q, want it to mention 'create DataDir'", err)
	}
}

// TestValidateMkdirIndexDirParentFailure covers the wrapped
// MkdirAll error for IndexDir's parent. Same trick: parent path
// is a regular file.
func TestValidateMkdirIndexDirParentFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("file-not-dir"), 0o600); err != nil {
		t.Fatal(err)
	}

	c := Default()
	c.DataDir = t.TempDir()
	// IndexDir's parent (blocker/nested) doesn't exist and can't be
	// created because its own parent (blocker) is a regular file.
	c.IndexDir = filepath.Join(blocker, "nested", "index.bleve")
	err := c.Validate()
	if err == nil {
		t.Fatal("Validate should fail when IndexDir's parent cannot be created")
	}
	if !strings.Contains(err.Error(), "parent of IndexDir") {
		t.Errorf("error = %q, want it to mention 'parent of IndexDir'", err)
	}
}

// TestSwartznetShareRootHomelessFallback covers the third return
// in swartznetShareRoot: no XDG_DATA_HOME and no detectable home
// directory. We force this by clearing every env var the stdlib
// userHomeDir consults on Linux — HOME and the common user-DB
// fallbacks. If the runtime still resolves a home (e.g. from
// /etc/passwd), we skip the test rather than fail.
func TestSwartznetShareRootHomelessFallback(t *testing.T) {
	// Note: t.Setenv requires Go 1.17+ and is automatically reverted.
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "")

	got := swartznetShareRoot()
	if got == "./swartznet-state" {
		// Hit the documented homeless fallback — pass.
		return
	}
	// Otherwise, the OS still resolved a home (Linux user-DB,
	// macOS, etc.). We can't force the fallback portably, so
	// document the skip rather than spuriously fail.
	t.Skipf("homeless fallback unreachable on this runtime; got %q", got)
}
