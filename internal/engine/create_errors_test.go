package engine_test

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/engine"
)

// TestCreateTorrentFileRenameFailure covers the rename-failure
// branch in CreateTorrentFile. We create the source content,
// then plant a non-empty directory at the requested outPath so
// os.Rename can't replace it with the regular tempfile.
func TestCreateTorrentFileRenameFailure(t *testing.T) {
	t.Parallel()
	eng := newTestEngine(t)

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "x.bin")
	if err := os.WriteFile(srcPath, []byte(fillTo(32*1024)), 0o644); err != nil {
		t.Fatal(err)
	}

	// Make outPath a non-empty directory so the rename step fails.
	outPath := filepath.Join(dir, "x.torrent")
	if err := os.Mkdir(outPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outPath, "blocker"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := eng.CreateTorrentFile(engine.CreateTorrentOptions{Root: srcPath}, outPath); err == nil {
		t.Error("CreateTorrentFile should fail when outPath is a non-empty directory")
	}
	// The leftover *.tmp must be cleaned up after the rename failure.
	if _, err := os.Stat(outPath + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("tempfile leaked: stat err = %v", err)
	}
}

// TestCreateTorrentFileMissingRootPropagates covers the
// `mi, err := e.CreateTorrent(opts); if err != nil` arm in
// CreateTorrentFile. Pass an opts.Root that doesn't exist so
// CreateTorrent fails on os.Stat — the wrapping function must
// surface the error without writing any file.
func TestCreateTorrentFileMissingRootPropagates(t *testing.T) {
	t.Parallel()
	eng := newTestEngine(t)
	dir := t.TempDir()
	missing := filepath.Join(dir, "no-such-root")
	out := filepath.Join(dir, "out.torrent")
	if _, _, err := eng.CreateTorrentFile(engine.CreateTorrentOptions{Root: missing}, out); err == nil {
		t.Error("CreateTorrentFile should fail when Root is missing")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf(".torrent should not exist after failure, stat err = %v", err)
	}
}

// TestCreateTorrentFileBadSigningKeyPropagatesError covers the
// "sign:" wrapped error branch in CreateTorrentFile: SignWith
// receives a private key of wrong length so signing.SignBytes
// errors and the function returns immediately without writing
// any file.
func TestCreateTorrentFileBadSigningKeyPropagatesError(t *testing.T) {
	t.Parallel()
	eng := newTestEngine(t)

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "x.bin")
	if err := os.WriteFile(srcPath, []byte(fillTo(32*1024)), 0o644); err != nil {
		t.Fatal(err)
	}
	torrentPath := filepath.Join(dir, "x.torrent")

	// Hand SignBytes a too-short private key so it errors.
	bogusKey := ed25519.PrivateKey{1, 2, 3}

	if _, _, err := eng.CreateTorrentFile(engine.CreateTorrentOptions{
		Root:     srcPath,
		SignWith: bogusKey,
	}, torrentPath); err == nil {
		t.Error("CreateTorrentFile should fail when SignWith is invalid")
	}
	// Nothing should have been written to the requested outPath.
	if _, err := os.Stat(torrentPath); !os.IsNotExist(err) {
		t.Errorf("torrent file should not exist after sign failure, stat err = %v", err)
	}
}
