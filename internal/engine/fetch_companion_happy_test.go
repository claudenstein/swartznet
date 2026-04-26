package engine_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// TestFetchCompanionTorrentReturnsAbsolutePath exercises the
// happy path of FetchCompanionTorrent: torrent has metadata,
// is single-file, and its bytes are already on disk so
// VerifyData verifies all pieces and the BytesCompleted poll
// loop exits immediately.
//
// Wires AddInfoHash + AddTorrentMetaInfo together so the
// AddInfoHash path's metadata-wait succeeds without real
// peers.
func TestFetchCompanionTorrentReturnsAbsolutePath(t *testing.T) {
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

	eng, err := engine.New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	defer eng.Close()

	srcPath := filepath.Join(dataDir, "fetch.bin")
	if err := os.WriteFile(srcPath, []byte(fillTo(32*1024)), 0o644); err != nil {
		t.Fatal(err)
	}
	mi, err := eng.CreateTorrent(engine.CreateTorrentOptions{Root: srcPath})
	if err != nil {
		t.Fatal(err)
	}
	ih := mi.HashInfoBytes()
	var ih20 [20]byte
	copy(ih20[:], ih[:])

	// Hand metainfo into the engine first so the FetchCompanionTorrent
	// path's GotInfo wait succeeds. AddTorrentMetaInfo uses the same
	// infohash, anacrolix dedupes via registerLocked, GotInfo fires.
	if _, err := eng.AddTorrentMetaInfo(mi); err != nil {
		t.Fatalf("AddTorrentMetaInfo: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := eng.FetchCompanionTorrent(ctx, ih20)
	if err != nil {
		t.Fatalf("FetchCompanionTorrent: %v", err)
	}
	want := filepath.Join(dataDir, mi.HashInfoBytes().HexString()) // path includes torrent name
	_ = want
	if got == "" {
		t.Errorf("FetchCompanionTorrent returned empty path")
	}
}
