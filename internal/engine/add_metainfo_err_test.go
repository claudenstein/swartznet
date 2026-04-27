package engine_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// TestAddTorrentMetaInfoEmptyInfoBytes covers AddTorrentMetaInfo's
// `t, err := e.client.AddTorrent(mi); if err != nil { return … }`
// arm. A metainfo with empty InfoBytes triggers anacrolix's
// metainfo-validation err before the engine handle is built.
func TestAddTorrentMetaInfoEmptyInfoBytes(t *testing.T) {
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

	// Empty-InfoBytes metainfo — anacrolix rejects on AddTorrent.
	mi := &metainfo.MetaInfo{InfoBytes: nil}
	if _, err := eng.AddTorrentMetaInfo(mi); err == nil {
		t.Error("AddTorrentMetaInfo should fail on empty InfoBytes")
	}
}
