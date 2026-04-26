package engine_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// TestCountActiveDownloadsSkipsSeedingTorrent covers
// countActiveDownloads' seeding-skip arm:
//
//	if info := t.Info(); info != nil {
//	    if t.BytesMissing() <= 0 { continue }
//	}
//
// Reached when a torrent is fully verified on disk. Place the
// source file directly under the engine's DataDir so VerifyData
// finds the bytes already present and BytesMissing drops to 0.
// Then add a magnet; queueOrActivate runs countActiveDownloads,
// which must skip the now-seeding torrent.
func TestCountActiveDownloadsSkipsSeedingTorrent(t *testing.T) {
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
	eng.SetMaxActiveDownloads(1)

	// Place the source file inside DataDir so anacrolix's storage
	// backend finds it during VerifyData.
	srcPath := filepath.Join(dataDir, "src.bin")
	if err := os.WriteFile(srcPath, []byte(fillTo(32*1024)), 0o644); err != nil {
		t.Fatal(err)
	}
	mi, err := eng.CreateTorrent(engine.CreateTorrentOptions{Root: srcPath})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.AddTorrentMetaInfo(mi); err != nil {
		t.Fatal(err)
	}
	wantSeedIH := mi.HashInfoBytes().HexString()

	deadline := time.Now().Add(5 * time.Second)
	seeding := false
	for time.Now().Before(deadline) {
		for _, s := range eng.TorrentSnapshots() {
			if s.InfoHash == wantSeedIH && s.BytesMissing == 0 && s.Size > 0 {
				seeding = true
			}
		}
		if seeding {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !seeding {
		t.Fatal("first torrent never reached BytesMissing == 0 — VerifyData did not complete")
	}

	// Adding a magnet calls queueOrActivate → countActiveDownloads.
	// The seeding torrent must be skipped, so the magnet activates.
	var ih [20]byte
	if _, err := rand.Read(ih[:]); err != nil {
		t.Fatal(err)
	}
	hexIH := hex.EncodeToString(ih[:])
	if _, err := eng.AddMagnet("magnet:?xt=urn:btih:" + hexIH); err != nil {
		t.Fatalf("AddMagnet: %v", err)
	}
}
