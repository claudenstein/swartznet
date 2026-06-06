package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestLoadPrivKeyExplicitPathLoadsExisting covers loadPrivKey's
// explicit-path branch when the file exists: it loads the on-disk
// keypair and round-trips to the same bytes.
func TestLoadPrivKeyExplicitPathLoadsExisting(t *testing.T) {
	t.Parallel()
	keyPath := filepath.Join(t.TempDir(), "id.key")
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, priv, 0o600); err != nil {
		t.Fatal(err)
	}

	gotPriv, gotPub, err := loadPrivKey(keyPath)
	if err != nil {
		t.Fatalf("loadPrivKey: %v", err)
	}
	if string(gotPriv) != string(priv) || string(gotPub) != string(pub) {
		t.Error("loadPrivKey did not return the on-disk keypair")
	}
}

// TestLoadPrivKeyExplicitPathFailsClosed is the regression test for
// the silent-mint bug: an explicit --key pointing at a missing file
// must error (and unwrap to os.ErrNotExist), never generate a fresh
// untrusted identity. Before the fix loadPrivKey called
// identity.LoadOrCreate, which created a brand-new keypair here.
func TestLoadPrivKeyExplicitPathFailsClosed(t *testing.T) {
	t.Parallel()
	keyPath := filepath.Join(t.TempDir(), "missing.key")

	_, _, err := loadPrivKey(keyPath)
	if err == nil {
		t.Fatal("loadPrivKey must fail closed on a missing explicit key path, not mint a new identity")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error = %v, want it to unwrap to os.ErrNotExist", err)
	}
	// The bug's tell: a key file silently created at the path.
	if _, statErr := os.Stat(keyPath); statErr == nil {
		t.Error("loadPrivKey created a key file at a missing explicit path (should fail closed)")
	}
}

// TestLoadPrivKeyExplicitPathError covers loadPrivKey's
// `if err != nil → return` arm in the explicit-path branch.
// Plant a directory at the keyPath so loading fails.
func TestLoadPrivKeyExplicitPathError(t *testing.T) {
	t.Parallel()
	keyDir := filepath.Join(t.TempDir(), "id.key")
	if err := os.Mkdir(keyDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, _, err := loadPrivKey(keyDir); err == nil {
		t.Error("loadPrivKey should fail when keyPath is a directory")
	}
}

// TestLoadPrivKeyDefaultPath exercises loadPrivKey's empty-path
// branch (keyPath == "" → config.Default().IdentityPath →
// LoadOrCreate). Override XDG_DATA_HOME so the test never touches the
// user's real identity file, and assert the auto-created key lands
// under that XDG root — locking the daemon/CLI default agreement.
func TestLoadPrivKeyDefaultPath(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)

	priv, pub, err := loadPrivKey("")
	if err != nil {
		t.Fatalf("loadPrivKey(\"\"): %v", err)
	}
	if len(priv) == 0 || len(pub) == 0 {
		t.Errorf("loadPrivKey returned empty keypair")
	}
	want := filepath.Join(xdg, "swartznet", "identity.key")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("default identity not created at XDG path %q: %v", want, err)
	}
}

// TestLoadPrivKeyDefaultPathLoadFails covers the empty-keyPath
// branch's `id, err := identity.LoadOrCreate(path); if err != nil {
// return nil, nil, err }` arm. XDG_DATA_HOME points at a regular
// file, so the swartznet parent dir can't be MkdirAll'd →
// LoadOrCreate fails → loadPrivKey surfaces the err.
func TestLoadPrivKeyDefaultPathLoadFails(t *testing.T) {
	dir := t.TempDir()
	xdgAsFile := filepath.Join(dir, "fakexdg")
	if err := os.WriteFile(xdgAsFile, []byte("regular file, not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", xdgAsFile)

	if _, _, err := loadPrivKey(""); err == nil {
		t.Error("loadPrivKey should fail when default-path MkdirAll can't run")
	}
}
