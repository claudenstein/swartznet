package engine_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// newAddTorrentFileEngine spins up the standard hermetic engine.
func newAddTorrentFileEngine(t *testing.T) (*engine.Engine, func()) {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
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
	return eng, func() { _ = eng.Close() }
}

// TestAddTorrentFileMissingPath covers the ReadFile error branch.
func TestAddTorrentFileMissingPath(t *testing.T) {
	t.Parallel()
	eng, cleanup := newAddTorrentFileEngine(t)
	defer cleanup()

	if _, err := eng.AddTorrentFile(filepath.Join(t.TempDir(), "missing.torrent")); err == nil {
		t.Error("AddTorrentFile on missing path should error")
	}
}

// TestAddTorrentFileGarbageContents covers the metainfo.Load
// error branch: the file exists but isn't a bencoded metainfo.
func TestAddTorrentFileGarbageContents(t *testing.T) {
	t.Parallel()
	eng, cleanup := newAddTorrentFileEngine(t)
	defer cleanup()

	path := filepath.Join(t.TempDir(), "garbage.torrent")
	if err := os.WriteFile(path, []byte("not bencode"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.AddTorrentFile(path); err == nil {
		t.Error("AddTorrentFile on garbage contents should error")
	}
}

// TestAddTorrentFileAfterCloseFails covers the closed-engine
// guard.
func TestAddTorrentFileAfterCloseFails(t *testing.T) {
	t.Parallel()
	eng, _ := newAddTorrentFileEngine(t)

	// Build a real torrent first while the engine is open.
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.bin")
	if err := os.WriteFile(srcPath, []byte(fillTo(32*1024)), 0o644); err != nil {
		t.Fatal(err)
	}
	torrentPath := filepath.Join(dir, "x.torrent")
	if _, _, err := eng.CreateTorrentFile(engine.CreateTorrentOptions{Root: srcPath}, torrentPath); err != nil {
		t.Fatalf("CreateTorrentFile: %v", err)
	}

	if err := eng.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := eng.AddTorrentFile(torrentPath); err == nil {
		t.Error("AddTorrentFile after Close should error")
	}
}
