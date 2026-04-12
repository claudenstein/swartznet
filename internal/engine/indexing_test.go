package engine_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// TestSetTorrentIndexingUnknownInfohash verifies SetTorrentIndexing
// returns an error for an infohash the engine has not seen.
func TestSetTorrentIndexingUnknownInfohash(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(context.Background(), cfg, log)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	defer eng.Close()

	unknownIH := "0000000000000000000000000000000000000000"
	if err := eng.SetTorrentIndexing(unknownIH, false); err == nil {
		t.Fatal("expected error for unknown infohash, got nil")
	}
}

// TestSetTorrentIndexingReflectedInSnapshot verifies that toggling
// the per-torrent indexing flag is reflected in TorrentSnapshots.
func TestSetTorrentIndexingReflectedInSnapshot(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(context.Background(), cfg, log)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	defer eng.Close()

	// A fixed 20-byte infohash so AddInfoHash doesn't need metadata.
	var ih [20]byte
	for i := range ih {
		ih[i] = byte(i + 1)
	}
	h, err := eng.AddInfoHash(ih)
	if err != nil {
		t.Fatalf("AddInfoHash: %v", err)
	}
	ihHex := h.T.InfoHash().HexString()

	// Default: indexing is on.
	snaps := eng.TorrentSnapshots()
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snaps))
	}
	if !snaps[0].Indexing {
		t.Fatal("new torrent should default to Indexing=true")
	}

	// Flip it off.
	if err := eng.SetTorrentIndexing(ihHex, false); err != nil {
		t.Fatalf("SetTorrentIndexing off: %v", err)
	}
	snaps = eng.TorrentSnapshots()
	if snaps[0].Indexing {
		t.Fatal("after SetTorrentIndexing(false), snapshot should show Indexing=false")
	}

	// Flip it back on (idempotent-ish: we just verify the flag).
	if err := eng.SetTorrentIndexing(ihHex, true); err != nil {
		t.Fatalf("SetTorrentIndexing on: %v", err)
	}
	snaps = eng.TorrentSnapshots()
	if !snaps[0].Indexing {
		t.Fatal("after SetTorrentIndexing(true), snapshot should show Indexing=true")
	}
}
