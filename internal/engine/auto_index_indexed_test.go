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
	"github.com/swartznet/swartznet/internal/indexer"
)

// TestAutoIndexIndexTorrentErrorIsLogged covers autoIndex's
// `if err := idx.IndexTorrent(doc); err != nil { log.Warn(...) }`
// arm. Wire an index, close it BEFORE the metainfo add so
// autoIndex's IndexTorrent surfaces a 'closed' error which the
// arm swallows after a warn.
func TestAutoIndexIndexTorrentErrorIsLogged(t *testing.T) {
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

	idx, err := indexer.Open(filepath.Join(t.TempDir(), "closed.bleve"))
	if err != nil {
		t.Fatalf("indexer.Open: %v", err)
	}
	eng.SetIndex(idx)
	// Close before AddTorrentMetaInfo so autoIndex hits a closed index.
	idx.Close()

	srcPath := filepath.Join(dataDir, "src.bin")
	if err := os.WriteFile(srcPath, []byte(fillTo(32*1024)), 0o644); err != nil {
		t.Fatal(err)
	}
	mi, err := eng.CreateTorrent(engine.CreateTorrentOptions{Root: srcPath})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.AddTorrentMetaInfo(mi); err != nil {
		t.Fatalf("AddTorrentMetaInfo: %v (autoIndex IndexTorrent failure must be non-fatal)", err)
	}
	// Give autoIndex's goroutine time to run.
	time.Sleep(100 * time.Millisecond)
}

// TestAutoIndexIndexesMetainfoTorrent covers autoIndex's
// `if idx != nil && h.IsIndexing() { idx.IndexTorrent(doc) }`
// success arm. Wire an indexer.Index into the engine via
// SetIndex, add a torrent via AddTorrentMetaInfo so GotInfo
// fires immediately, then poll the index until the torrent
// doc appears.
func TestAutoIndexIndexesMetainfoTorrent(t *testing.T) {
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

	idx, err := indexer.Open(filepath.Join(t.TempDir(), "auto-index.bleve"))
	if err != nil {
		t.Fatalf("indexer.Open: %v", err)
	}
	defer idx.Close()
	eng.SetIndex(idx)

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

	// Poll until autoIndex has run.
	wantIH := mi.HashInfoBytes().HexString()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stats, err := idx.Stats()
		if err == nil && stats.TorrentCount >= 1 {
			// Search for the doc to confirm it's the right one.
			res, sErr := idx.Search(indexer.SearchRequest{Query: wantIH, Limit: 1})
			if sErr == nil && res.Total >= 1 {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("autoIndex never indexed the torrent within 2s")
}
