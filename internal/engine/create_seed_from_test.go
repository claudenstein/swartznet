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

// TestAddTorrentMetaInfoSeedFromExternalPath reproduces the
// "Create Torrent shows 0%" bug. anacrolix's default storage is
// rooted at cfg.DataDir; if the user's Create Torrent flow
// hashes content sitting outside that directory and we add the
// metainfo without a storage override, VerifyData runs against
// the wrong path and the row sticks at 0% even though the
// bytes are clearly on disk. AddTorrentMetaInfoSeedFrom must
// install a per-torrent storage rooted at the source's parent
// so anacrolix locates the real file.
func TestAddTorrentMetaInfoSeedFromExternalPath(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	srcParent := t.TempDir() // distinct from dataDir on purpose
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

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	eng, err := engine.New(ctx, cfg, log)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	defer eng.Close()

	// Place the source file in srcParent (NOT dataDir). This is
	// the realistic Create Torrent scenario: the user picks a
	// file in ~/Documents and the engine's DataDir lives in
	// ~/.local/share/swartznet/data.
	srcPath := filepath.Join(srcParent, "fixture.bin")
	payload := make([]byte, 64*1024)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	if err := os.WriteFile(srcPath, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	mi, err := eng.CreateTorrent(engine.CreateTorrentOptions{Root: srcPath})
	if err != nil {
		t.Fatalf("CreateTorrent: %v", err)
	}
	wantIH := mi.HashInfoBytes().HexString()

	// SeedFrom variant points the storage at srcParent so anacrolix
	// finds the bytes that already live there.
	if _, err := eng.AddTorrentMetaInfoSeedFrom(mi, srcParent); err != nil {
		t.Fatalf("AddTorrentMetaInfoSeedFrom: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	complete := false
	var seen engine.TorrentSnapshot
	for time.Now().Before(deadline) {
		for _, s := range eng.TorrentSnapshots() {
			if s.InfoHash == wantIH {
				seen = s
				if s.Size > 0 && s.BytesMissing == 0 {
					complete = true
				}
			}
		}
		if complete {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !complete {
		t.Fatalf("seeded torrent never reached 0 missing bytes; last snapshot: %+v", seen)
	}
}

// TestAddTorrentMetaInfoSeedFromSurvivesRestart locks in the
// session-side half of the same fix: DataPath must be persisted
// so RestoreSession reapplies the per-torrent storage and the
// restored handle continues seeding from the source instead of
// jumping back to 0%.
func TestAddTorrentMetaInfoSeedFromSurvivesRestart(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	srcParent := t.TempDir()
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

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	srcPath := filepath.Join(srcParent, "fixture.bin")
	payload := make([]byte, 64*1024)
	for i := range payload {
		payload[i] = byte((i * 7) % 251)
	}
	if err := os.WriteFile(srcPath, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	eng1, err := engine.New(ctx, cfg, log)
	if err != nil {
		t.Fatalf("engine.New (#1): %v", err)
	}
	mi, err := eng1.CreateTorrent(engine.CreateTorrentOptions{Root: srcPath})
	if err != nil {
		t.Fatalf("CreateTorrent: %v", err)
	}
	if _, err := eng1.AddTorrentMetaInfoSeedFrom(mi, srcParent); err != nil {
		t.Fatalf("AddTorrentMetaInfoSeedFrom: %v", err)
	}
	wantIH := mi.HashInfoBytes().HexString()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ok := false
		for _, s := range eng1.TorrentSnapshots() {
			if s.InfoHash == wantIH && s.Size > 0 && s.BytesMissing == 0 {
				ok = true
			}
		}
		if ok {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := eng1.Close(); err != nil {
		t.Fatalf("eng1 Close: %v", err)
	}

	eng2, err := engine.New(ctx, cfg, log)
	if err != nil {
		t.Fatalf("engine.New (#2): %v", err)
	}
	defer eng2.Close()
	if err := eng2.RestoreSession(); err != nil {
		t.Fatalf("RestoreSession: %v", err)
	}

	deadline = time.Now().Add(10 * time.Second)
	rehashed := false
	var seen engine.TorrentSnapshot
	for time.Now().Before(deadline) {
		for _, s := range eng2.TorrentSnapshots() {
			if s.InfoHash == wantIH {
				seen = s
				if s.Size > 0 && s.BytesMissing == 0 {
					rehashed = true
				}
			}
		}
		if rehashed {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !rehashed {
		t.Fatalf("restored torrent did not rehash from external source; last snapshot: %+v", seen)
	}
}
