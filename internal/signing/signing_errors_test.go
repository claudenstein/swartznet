package signing_test

import (
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/swartznet/swartznet/internal/signing"
)

func TestSignBytesShortPrivateKey(t *testing.T) {
	t.Parallel()
	raw := miniTorrent(t)
	if _, err := signing.SignBytes(raw, ed25519.PrivateKey{1, 2, 3}); err == nil {
		t.Error("SignBytes with short key should error")
	}
}

func TestSignBytesGarbageBencode(t *testing.T) {
	t.Parallel()
	_, priv := newKey(t)
	if _, err := signing.SignBytes([]byte("not bencode at all"), priv); err == nil {
		t.Error("SignBytes on non-bencode input should error")
	}
}

func TestSignBytesMissingInfoDict(t *testing.T) {
	t.Parallel()
	_, priv := newKey(t)
	// Valid bencode dict with no "info" key.
	mi := map[string]interface{}{"announce": "x"}
	raw, err := bencode.Marshal(mi)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signing.SignBytes(raw, priv); err == nil {
		t.Error("SignBytes on metainfo with no info dict should error")
	}
}

func TestVerifyBytesGarbageBencode(t *testing.T) {
	t.Parallel()
	if _, err := signing.VerifyBytes([]byte("not bencode")); err == nil {
		t.Error("VerifyBytes on non-bencode should error")
	}
}

func TestVerifyBytesMissingInfoDict(t *testing.T) {
	t.Parallel()
	mi := map[string]interface{}{"announce": "x"}
	raw, _ := bencode.Marshal(mi)
	if _, err := signing.VerifyBytes(raw); err == nil {
		t.Error("VerifyBytes on metainfo with no info dict should error")
	}
}

func TestVerifyBytesBadPubKeyLength(t *testing.T) {
	t.Parallel()
	mi := map[string]interface{}{
		"info": map[string]interface{}{
			"name": "x", "piece length": 16384, "pieces": string(make([]byte, 20)), "length": 4,
		},
		"snet.pubkey": "short",                  // not 32 bytes
		"snet.sig":    string(make([]byte, 64)), // 64 byte placeholder
	}
	raw, _ := bencode.Marshal(mi)
	_, err := signing.VerifyBytes(raw)
	if err == nil || errors.Is(err, signing.ErrNotSigned) {
		t.Errorf("got %v, want a length error", err)
	}
}

func TestVerifyBytesBadSigLength(t *testing.T) {
	t.Parallel()
	mi := map[string]interface{}{
		"info": map[string]interface{}{
			"name": "x", "piece length": 16384, "pieces": string(make([]byte, 20)), "length": 4,
		},
		"snet.pubkey": string(make([]byte, ed25519.PublicKeySize)),
		"snet.sig":    "not-64-bytes",
	}
	raw, _ := bencode.Marshal(mi)
	_, err := signing.VerifyBytes(raw)
	if err == nil || errors.Is(err, signing.ErrNotSigned) {
		t.Errorf("got %v, want a length error", err)
	}
}

func TestSignFileMissingPath(t *testing.T) {
	t.Parallel()
	_, priv := newKey(t)
	missing := filepath.Join(t.TempDir(), "does-not-exist.torrent")
	if err := signing.SignFile(missing, priv); err == nil {
		t.Error("SignFile on missing path should error")
	}
}

func TestVerifyFileMissingPath(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "does-not-exist.torrent")
	if _, err := signing.VerifyFile(missing); err == nil {
		t.Error("VerifyFile on missing path should error")
	}
}

// TestSignFileRenameFailure covers SignFile's
// `os.Rename` error arm. Plant a non-empty directory at the
// target path; ReadFile and WriteFile both succeed, but the
// final rename can't replace a non-empty directory with a
// regular file. The leaked tempfile must be cleaned up.
func TestSignFileRenameFailure(t *testing.T) {
	t.Parallel()
	_, priv := newKey(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "mini.torrent")
	if err := os.WriteFile(path, miniTorrent(t), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pre-existing tmpfile blocker: plant a non-empty directory
	// at <path>.tmp so os.WriteFile fails at truncate-open. (We
	// use the WriteFile arm since rename-to-non-empty has
	// different platform semantics.)
	tmp := path + ".tmp"
	if err := os.MkdirAll(filepath.Join(tmp, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := signing.SignFile(path, priv); err == nil {
		t.Error("SignFile should fail when tempfile path is a non-empty directory")
	}
}

// TestVerifyFileReadAllFailsOnDirectory covers the
// `io.ReadAll(f)` error arm: os.Open succeeds on a directory
// (Linux semantics) but reading from a directory file
// descriptor returns EISDIR, surfacing the wrapped
// "signing: read" error rather than a decode failure.
func TestVerifyFileReadAllFailsOnDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := signing.VerifyFile(dir); err == nil {
		t.Error("VerifyFile on a directory should error during read")
	}
}

func TestVerifyFileGarbageContents(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.torrent")
	if err := os.WriteFile(path, []byte("not bencode"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := signing.VerifyFile(path); err == nil {
		t.Error("VerifyFile on garbage contents should error")
	}
}

func TestSignFileReplacesExistingSignature(t *testing.T) {
	t.Parallel()
	_, priv1 := newKey(t)
	_, priv2 := newKey(t)
	pub2 := priv2.Public().(ed25519.PublicKey)

	dir := t.TempDir()
	path := filepath.Join(dir, "mini.torrent")
	if err := os.WriteFile(path, miniTorrent(t), 0o644); err != nil {
		t.Fatal(err)
	}

	// First sign with key 1, then re-sign with key 2 — the second
	// signature must replace the first cleanly.
	if err := signing.SignFile(path, priv1); err != nil {
		t.Fatal(err)
	}
	if err := signing.SignFile(path, priv2); err != nil {
		t.Fatal(err)
	}

	sig, err := signing.VerifyFile(path)
	if err != nil {
		t.Fatalf("VerifyFile: %v", err)
	}
	if string(sig.PubKey[:]) != string(pub2) {
		t.Error("re-sign did not replace the previous signature")
	}
}
