package engine_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// TestFetchCompanionTorrentRejectsMultiFile — when an infohash
// already in the engine resolves to a multi-file torrent, the
// "want exactly 1" guard fires. Companion torrents always carry
// a single file (the index payload); anything else is a sign
// the publisher pointed at the wrong infohash.
func TestFetchCompanionTorrentRejectsMultiFile(t *testing.T) {
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
	t.Cleanup(func() { _ = eng.Close() })

	// Build a multi-file torrent.
	dir := t.TempDir()
	root := filepath.Join(dir, "content")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte(strings.Repeat("a", 16*1024+1))
	for _, name := range []string{"f1.bin", "f2.bin"} {
		if err := os.WriteFile(filepath.Join(root, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	torrentPath := filepath.Join(dir, "multi.torrent")
	ihHex, _, err := eng.CreateTorrentFile(engine.CreateTorrentOptions{Root: root}, torrentPath)
	if err != nil {
		t.Fatalf("CreateTorrentFile: %v", err)
	}
	if _, err := eng.AddTorrentFile(torrentPath); err != nil {
		t.Fatalf("AddTorrentFile: %v", err)
	}

	// Convert hex IH → [20]byte.
	var ih [20]byte
	for i := 0; i < 20; i++ {
		var b byte
		for j := 0; j < 2; j++ {
			c := ihHex[i*2+j]
			switch {
			case c >= '0' && c <= '9':
				b = b<<4 | (c - '0')
			case c >= 'a' && c <= 'f':
				b = b<<4 | (c - 'a' + 10)
			case c >= 'A' && c <= 'F':
				b = b<<4 | (c - 'A' + 10)
			}
		}
		ih[i] = b
	}

	// FetchCompanionTorrent on the same IH — handle exists,
	// metadata is local, GotInfo fires immediately, then the
	// files-count check sees 2 and errors out.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = eng.FetchCompanionTorrent(ctx, ih)
	if err == nil {
		t.Fatal("FetchCompanionTorrent should reject multi-file metainfo")
	}
	if !strings.Contains(err.Error(), "want exactly 1") {
		t.Errorf("err = %v, want 'want exactly 1' message", err)
	}
}
