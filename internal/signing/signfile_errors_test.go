package signing_test

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/signing"
)

// TestSignFileOnDirectoryPathFailsToRead covers SignFile's
// ReadFile-error branch. Pointing the path at a directory makes
// os.ReadFile error before SignBytes runs, and the wrapped
// "signing: read" error must propagate.
func TestSignFileOnDirectoryPathFailsToRead(t *testing.T) {
	t.Parallel()
	_, priv := newKey(t)
	dir := t.TempDir()
	dstPath := filepath.Join(dir, "dst")
	if err := os.Mkdir(dstPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := signing.SignFile(dstPath, priv); err == nil {
		t.Error("SignFile on a directory path should error")
	}
}

// TestSignFileWithCorruptInputPropagatesSignBytesError covers
// the SignBytes-error branch in SignFile: the file contents
// are not valid bencode, so SignBytes errors and SignFile
// returns the wrapped failure without writing any tempfile.
func TestSignFileWithCorruptInputPropagatesSignBytesError(t *testing.T) {
	t.Parallel()
	_, priv := newKey(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "garbage.torrent")
	if err := os.WriteFile(path, []byte("not bencode"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := signing.SignFile(path, priv); err == nil {
		t.Error("SignFile on garbage contents should error")
	}
	// Tempfile must not be left behind.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("tempfile leaked: stat err = %v", err)
	}
}

// TestSignFileShortKey covers the SignBytes-rejects-short-key
// path via SignFile (the existing TestSignBytesShortPrivateKey
// hits SignBytes directly; SignFile's wrapper has its own line
// to cover).
func TestSignFileShortKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "mini.torrent")
	if err := os.WriteFile(path, miniTorrent(t), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := signing.SignFile(path, ed25519.PrivateKey{1, 2, 3}); err == nil {
		t.Error("SignFile with short key should error")
	}
}
