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

// TestRestoreSessionRehashesOnDiskData reproduces the
// "torrents lose their progress after restart" report. We seed
// real bytes into engine #1, wait for the post-create
// VerifyData to mark them complete, close, then re-open with
// engine #2 + RestoreSession. Without verifyOnRestore,
// anacrolix would lazily wait for a peer request before
// touching storage and the snapshot would show 0 bytes
// completed; with the fix the rehash discovers the on-disk
// pieces and the bar lights up at 100% within a few seconds of
// the restore returning.
func TestRestoreSessionRehashesOnDiskData(t *testing.T) {
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

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Engine #1: produce a small torrent + on-disk content.
	eng1, err := engine.New(ctx, cfg, log)
	if err != nil {
		t.Fatalf("engine.New (#1): %v", err)
	}

	srcPath := filepath.Join(dataDir, "fixture.bin")
	payload := make([]byte, 64*1024)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	if err := os.WriteFile(srcPath, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	mi, err := eng1.CreateTorrent(engine.CreateTorrentOptions{Root: srcPath})
	if err != nil {
		t.Fatalf("CreateTorrent: %v", err)
	}
	if _, err := eng1.AddTorrentMetaInfo(mi); err != nil {
		t.Fatalf("AddTorrentMetaInfo: %v", err)
	}
	wantIH := mi.HashInfoBytes().HexString()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ok := false
		for _, s := range eng1.TorrentSnapshots() {
			if s.InfoHash == wantIH && s.BytesMissing == 0 && s.Size > 0 {
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

	// Engine #2: same DataDir, restore. Without verifyOnRestore
	// the snapshot would show BytesMissing == Size; with it the
	// rehash discovers the existing pieces and BytesMissing → 0.
	eng2, err := engine.New(ctx, cfg, log)
	if err != nil {
		t.Fatalf("engine.New (#2): %v", err)
	}
	defer eng2.Close()
	if err := eng2.RestoreSession(); err != nil {
		t.Fatalf("RestoreSession: %v", err)
	}

	deadline = time.Now().Add(10 * time.Second)
	var seen engine.TorrentSnapshot
	rehashed := false
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
		t.Fatalf("restored torrent never rehashed back to 0 missing bytes; last snapshot: %+v", seen)
	}
}
