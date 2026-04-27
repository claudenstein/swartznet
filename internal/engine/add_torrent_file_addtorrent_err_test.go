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

// TestAddTorrentFileAddTorrentErr covers AddTorrentFile's
// `t, err := e.client.AddTorrent(mi); if err != nil { return … }`
// arm at lines 1124-1128. Build a metainfo with empty InfoBytes,
// write it to disk as a "torrent" file, and pass it to
// AddTorrentFile. metainfo.Load happens first (succeeds on
// well-formed bencode), then anacrolix's AddTorrent rejects.
func TestAddTorrentFileAddTorrentErr(t *testing.T) {
	t.Parallel()
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
	defer eng.Close()

	// Hand-build raw bencode for a metainfo whose info dict is
	// just a single integer ("0") — well-formed bencode but
	// not a valid info dict, so anacrolix's AddTorrent rejects.
	body := []byte("d4:infoi0ee")
	path := filepath.Join(t.TempDir(), "bad.torrent")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := eng.AddTorrentFile(path); err == nil {
		t.Error("AddTorrentFile should fail on metainfo with empty InfoBytes")
	}
}
