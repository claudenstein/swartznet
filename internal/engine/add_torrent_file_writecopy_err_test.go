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

// TestAddTorrentFileTolerateWriteCopyError covers AddTorrentFile's
// `tname, werr := e.sess.writeTorrentCopy(...); if werr != nil { log.Warn(...) }`
// arm. After engine.New has created the torrents/ dir, strip
// write permission on it so writeTorrentCopy's WriteFile call
// fails. AddTorrentFile must still succeed (the copy is best-
// effort) and return a non-nil handle.
//
// Skipped on Windows (chmod semantics) and as root (can write to
// 0o500 dirs anyway).
func TestAddTorrentFileTolerateWriteCopyError(t *testing.T) {
	t.Parallel()
	if os.Getuid() == 0 {
		t.Skip("running as root, chmod 0o500 doesn't deny writes")
	}
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

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.bin")
	if err := os.WriteFile(srcPath, []byte(fillTo(32*1024)), 0o644); err != nil {
		t.Fatal(err)
	}
	torrentPath := filepath.Join(dir, "x.torrent")
	if _, _, err := eng.CreateTorrentFile(engine.CreateTorrentOptions{Root: srcPath}, torrentPath); err != nil {
		t.Fatalf("CreateTorrentFile: %v", err)
	}

	// Locate the engine's torrents/ dir and chmod it read-execute
	// so writeTorrentCopy's WriteFile fails.
	torrentsDir := filepath.Join(dataDir, "torrents")
	if err := os.Chmod(torrentsDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(torrentsDir, 0o755) })

	h, err := eng.AddTorrentFile(torrentPath)
	if err != nil {
		t.Fatalf("AddTorrentFile: %v (writeTorrentCopy failure must be non-fatal)", err)
	}
	if h == nil {
		t.Error("expected non-nil handle from AddTorrentFile")
	}
}
