package trust_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/trust"
)

func TestLoadOrCreateMalformedJSON(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "trust.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := trust.LoadOrCreate(path); err == nil {
		t.Error("LoadOrCreate on malformed JSON should error")
	}
}

func TestLoadOrCreateUnreadablePath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// os.Open on a directory succeeds on Linux, then JSON decode
	// fails — either way LoadOrCreate must propagate the error
	// rather than silently start fresh.
	_, err := trust.LoadOrCreate(dir)
	if err == nil {
		t.Error("LoadOrCreate on a directory path should error")
	}
}

// TestLoadOrCreatePathTraverseFile covers the
// "os.Open fails with non-NotExist error" arm of LoadOrCreate
// (the existing TestLoadOrCreateUnreadablePath ends up hitting
// the JSON-decode error path instead, since os.Open on a
// directory succeeds on Linux). Plant a regular file at a
// component that should be a directory so os.Open fails with
// ENOTDIR, which is not os.ErrNotExist.
func TestLoadOrCreatePathTraverseFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Make `dir/file` a regular file, then ask for `dir/file/trust.json`.
	regular := filepath.Join(dir, "file")
	if err := os.WriteFile(regular, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(regular, "trust.json")
	if _, err := trust.LoadOrCreate(target); err == nil {
		t.Error("LoadOrCreate should fail when a path component is not a directory")
	}
}

// TestLabelOnUntrustedKeyReturnsEmpty covers the documented "empty
// string if not trusted" path of Label. The existing tests
// implicitly cover the trusted lookup; this one proves the miss
// returns "" rather than something like "(unknown)".
func TestLabelOnUntrustedKeyReturnsEmpty(t *testing.T) {
	t.Parallel()
	s, err := trust.LoadOrCreate("")
	if err != nil {
		t.Fatal(err)
	}
	got := s.Label(strings.Repeat("0", 64))
	if got != "" {
		t.Errorf("Label on untrusted key = %q, want empty", got)
	}
}

// TestSaveRenameFailure covers the rename-failure branch of
// the trust store. Add tries to persist; planting a non-empty
// directory at the path makes os.Rename fail, surfacing the
// wrapped "rename" error.
func TestSaveRenameFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "trust.json")

	// Build a store backed by the eventual path. Add the first
	// entry while the path is still writable, so we know the
	// happy path works.
	s, err := trust.LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	good := strings.Repeat("ab", 32)
	if err := s.Add(good, "first"); err != nil {
		t.Fatal(err)
	}

	// Now plant a non-empty directory at path so the next save
	// rename fails.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "blocker"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Subsequent Add triggers a save — must surface the wrapped
	// rename error and not panic.
	other := strings.Repeat("cd", 32)
	if err := s.Add(other, "doomed"); err == nil {
		t.Error("Add should fail when save's rename target is a non-empty directory")
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("tempfile leaked: stat err = %v", err)
	}
}
