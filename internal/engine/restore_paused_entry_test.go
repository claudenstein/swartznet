package engine_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// TestRestoreSessionPausedEntryDisablesData covers the
// previously-uncovered entry.Paused branch in restoreEntry.
// Hand-crafting a session manifest with a paused magnet entry
// and reopening the engine should restore that torrent in a
// paused state (DisallowDataDownload + DisallowDataUpload
// applied, mirror flag set).
func TestRestoreSessionPausedEntryDisablesData(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true
	cfg.IdentityPath = ""
	cfg.ReputationPath = ""
	cfg.SeedListPath = ""
	cfg.BloomPath = ""
	cfg.TrustPath = ""

	var ih [20]byte
	if _, err := rand.Read(ih[:]); err != nil {
		t.Fatal(err)
	}
	hexIH := hex.EncodeToString(ih[:])
	magnet := "magnet:?xt=urn:btih:" + hexIH

	writeSessionManifest(t, dataDir, []sessionEntryJSON{
		{
			InfoHash:  hexIH,
			AddedVia:  "magnet",
			MagnetURI: magnet,
			Paused:    true,
			Indexing:  true,
		},
	})

	eng, err := engine.New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	defer eng.Close()

	if err := eng.RestoreSession(); err != nil {
		t.Fatalf("RestoreSession: %v", err)
	}

	// Find our torrent and confirm it's paused.
	snaps := eng.TorrentSnapshots()
	if len(snaps) != 1 {
		t.Fatalf("len(snaps) = %d, want 1", len(snaps))
	}
	if !snaps[0].Paused {
		t.Errorf("Paused = false, want true (entry.Paused was set)")
	}
}

// TestRestoreSessionEntryWithSignedByPreserved covers the
// entry.SignedBy != "" branch. We can't actually sign during
// restore (the session metadata only stores the pubkey hex),
// but the engine should copy that hex onto the Handle.
func TestRestoreSessionEntryWithSignedByPreserved(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true
	cfg.IdentityPath = ""
	cfg.ReputationPath = ""
	cfg.SeedListPath = ""
	cfg.BloomPath = ""
	cfg.TrustPath = ""

	const pubHex = "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd"
	var ih [20]byte
	if _, err := rand.Read(ih[:]); err != nil {
		t.Fatal(err)
	}
	hexIH := hex.EncodeToString(ih[:])

	writeSessionManifest(t, dataDir, []sessionEntryJSON{
		{
			InfoHash: hexIH,
			AddedVia: "infohash",
			Indexing: true,
			SignedBy: pubHex,
		},
	})

	eng, err := engine.New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	defer eng.Close()

	if err := eng.RestoreSession(); err != nil {
		t.Fatalf("RestoreSession: %v", err)
	}

	snaps := eng.TorrentSnapshots()
	if len(snaps) != 1 {
		t.Fatalf("len(snaps) = %d, want 1", len(snaps))
	}
	if snaps[0].SignedBy != pubHex {
		t.Errorf("SignedBy = %q, want %q", snaps[0].SignedBy, pubHex)
	}

	// Sanity: temp dir avoids the trust file we configure,
	// so TrustedPublisher should be false.
	if snaps[0].TrustedPublisher {
		t.Error("TrustedPublisher should be false (no trust store configured)")
	}

	// Use the helper var so the import isn't unused in the
	// rare case t.Skip lands here.
	_ = filepath.Join("", "")
}
