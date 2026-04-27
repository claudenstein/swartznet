package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadPrivKeyExplicitPath covers loadPrivKey's explicit-path
// branch: with keyPath != "", LoadOrCreate is called directly
// against that path. On a fresh path it generates and persists a
// new keypair, so subsequent calls return the same one.
func TestLoadPrivKeyExplicitPath(t *testing.T) {
	t.Parallel()
	keyPath := filepath.Join(t.TempDir(), "id.key")

	priv, pub, err := loadPrivKey(keyPath)
	if err != nil {
		t.Fatalf("loadPrivKey: %v", err)
	}
	if len(priv) == 0 || len(pub) == 0 {
		t.Errorf("loadPrivKey returned empty keypair")
	}

	// Re-load — must round-trip to the same bytes.
	priv2, pub2, err := loadPrivKey(keyPath)
	if err != nil {
		t.Fatalf("loadPrivKey (round trip): %v", err)
	}
	if string(priv) != string(priv2) || string(pub) != string(pub2) {
		t.Error("loadPrivKey round-trip produced different keypair")
	}
}

// TestLoadPrivKeyExplicitPathError covers loadPrivKey's
// `if err != nil → return` arm in the explicit-path branch.
// Plant a directory at the keyPath so LoadOrCreate's read fails.
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
// branch (keyPath == "" → defaultIdentityPath → LoadOrCreate).
// Override HOME so the test never touches the user's real
// identity file.
func TestLoadPrivKeyDefaultPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	priv, pub, err := loadPrivKey("")
	if err != nil {
		t.Fatalf("loadPrivKey(\"\"): %v", err)
	}
	if len(priv) == 0 || len(pub) == 0 {
		t.Errorf("loadPrivKey returned empty keypair")
	}
}

// TestLoadPrivKeyDefaultPathHomeUnset covers the
// `defaultIdentityPath err → return nil, nil, err` arm of
// loadPrivKey, plus defaultIdentityPath's own UserHomeDir-err
// arm. Empty HOME makes os.UserHomeDir fail with
// "$HOME is not defined".
func TestLoadPrivKeyDefaultPathHomeUnset(t *testing.T) {
	t.Setenv("HOME", "")

	if _, _, err := loadPrivKey(""); err == nil {
		t.Error("loadPrivKey(\"\") should fail when HOME is unset")
	}
}

// TestLoadPrivKeyDefaultPathLoadFails covers the empty-keyPath
// branch's `id, err := identity.LoadOrCreate(path); if err !=
// nil { return nil, nil, err }` arm at lines 118-121. HOME
// points at a regular file, so the .local/share/swartznet
// parent dir can't be MkdirAll'd → LoadOrCreate fails → loadPrivKey
// surfaces the err.
func TestLoadPrivKeyDefaultPathLoadFails(t *testing.T) {
	dir := t.TempDir()
	homeAsFile := filepath.Join(dir, "fakehome")
	if err := os.WriteFile(homeAsFile, []byte("regular file, not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", homeAsFile)

	if _, _, err := loadPrivKey(""); err == nil {
		t.Error("loadPrivKey should fail when default-path MkdirAll can't run")
	}
}
