package identity_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/identity"
)

// TestLoadOrCreateMkdirParentIsAFile covers the MkdirAll error
// branch of LoadOrCreate. We plant a regular file at what would
// be the parent directory of the requested key path; MkdirAll
// then fails because its target is a file, not a directory.
func TestLoadOrCreateMkdirParentIsAFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Create a file at dir/parent. The key path becomes
	// dir/parent/id.key whose parent (dir/parent) is now a file —
	// MkdirAll on it will fail.
	parent := filepath.Join(dir, "parent")
	if err := os.WriteFile(parent, []byte("blocker"), 0o600); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(parent, "id.key")

	_, err := identity.LoadOrCreate(keyPath)
	if err == nil {
		t.Fatal("LoadOrCreate should fail when parent dir cannot be created")
	}
	if !strings.Contains(err.Error(), "mkdir") {
		t.Errorf("error = %q, want it to mention 'mkdir'", err.Error())
	}
}
