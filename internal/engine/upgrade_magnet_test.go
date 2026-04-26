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

// TestUpgradeMagnetSessionWritesTorrentCopy exercises
// engine.upgradeMagnetSession's happy path: GotInfo fires (via
// a follow-up AddTorrentMetaInfo for the same infohash), the
// goroutine writes the torrent copy under DataDir/torrents/,
// and the session entry is updated.
//
// AddInfoHash starts the upgradeMagnetSession goroutine. Then
// AddTorrentMetaInfo with the same infohash hands metadata to
// anacrolix, which fires GotInfo on the original handle.
func TestUpgradeMagnetSessionWritesTorrentCopy(t *testing.T) {
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

	// Build a real torrent so we have a metainfo to feed in
	// later.
	srcPath := filepath.Join(dataDir, "src.bin")
	if err := os.WriteFile(srcPath, []byte(fillTo(32*1024)), 0o644); err != nil {
		t.Fatal(err)
	}
	mi, err := eng.CreateTorrent(engine.CreateTorrentOptions{Root: srcPath})
	if err != nil {
		t.Fatal(err)
	}
	ih := mi.HashInfoBytes()

	// AddInfoHash spawns upgradeMagnetSession waiting for GotInfo.
	if _, err := eng.AddInfoHash(ih); err != nil {
		t.Fatalf("AddInfoHash: %v", err)
	}

	// AddTorrentMetaInfo with the same infohash hands the metadata
	// to anacrolix. registerLocked dedupes by infohash so the
	// existing handle is returned; metadata still flows to it.
	if _, err := eng.AddTorrentMetaInfo(mi); err != nil {
		t.Fatalf("AddTorrentMetaInfo: %v", err)
	}

	// Wait briefly for upgradeMagnetSession to run.
	torrentsDir := filepath.Join(dataDir, "torrents")
	wantPath := filepath.Join(torrentsDir, ih.HexString()+".torrent")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(wantPath); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("upgradeMagnetSession never wrote %s within 2s", wantPath)
}
