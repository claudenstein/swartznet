package engine_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/engine"
)

// TestCreateTorrentFileWithSigningRoundTrip creates a signed
// .torrent, adds it back via AddTorrentFile, and verifies that
// the engine populates the Handle.SignedBy field with the signing
// pubkey.
func TestCreateTorrentFileWithSigningRoundTrip(t *testing.T) {
	t.Parallel()

	eng := newTestEngine(t)

	// Generate an ephemeral signing key.
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}

	// Make a small file to hash.
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "x.bin")
	if err := os.WriteFile(srcPath, []byte(fillTo(32*1024)), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	torrentPath := filepath.Join(dir, "x.torrent")

	// Create signed.
	ih, _, err := eng.CreateTorrentFile(engine.CreateTorrentOptions{
		Root:     srcPath,
		SignWith: priv,
	}, torrentPath)
	if err != nil {
		t.Fatalf("CreateTorrentFile: %v", err)
	}
	if ih == "" {
		t.Fatal("empty infohash")
	}

	// Load it back.
	h, err := eng.AddTorrentFile(torrentPath)
	if err != nil {
		t.Fatalf("AddTorrentFile: %v", err)
	}

	wantPub := ""
	for _, b := range pub {
		const hex = "0123456789abcdef"
		wantPub += string([]byte{hex[b>>4], hex[b&0x0f]})
	}

	if h.SignedBy() != wantPub {
		t.Errorf("SignedBy mismatch: got %q, want %q", h.SignedBy(), wantPub)
	}

	// Also check that the snapshot surfaces it.
	snaps := eng.TorrentSnapshots()
	if len(snaps) == 0 {
		t.Fatal("no snapshots")
	}
	if snaps[0].SignedBy != wantPub {
		t.Errorf("snapshot SignedBy mismatch: got %q, want %q", snaps[0].SignedBy, wantPub)
	}
}

// TestCreateTorrentFileUnsignedNoSignedBy confirms that an
// unsigned create round-trip leaves SignedBy empty.
func TestCreateTorrentFileUnsignedNoSignedBy(t *testing.T) {
	t.Parallel()

	eng := newTestEngine(t)

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "x.bin")
	if err := os.WriteFile(srcPath, []byte(fillTo(32*1024)), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	torrentPath := filepath.Join(dir, "x.torrent")

	if _, _, err := eng.CreateTorrentFile(engine.CreateTorrentOptions{
		Root: srcPath,
		// No SignWith.
	}, torrentPath); err != nil {
		t.Fatalf("CreateTorrentFile: %v", err)
	}
	h, err := eng.AddTorrentFile(torrentPath)
	if err != nil {
		t.Fatalf("AddTorrentFile: %v", err)
	}
	if h.SignedBy() != "" {
		t.Errorf("expected empty SignedBy, got %q", h.SignedBy())
	}
}
