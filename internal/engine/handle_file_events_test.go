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

// TestHandleFileEventsAndSubscribeFileEventsAreSiblings covers
// both the FileEvents() legacy alias and the SubscribeFileEvents()
// preferred name. Each call MUST return a fresh, independent
// channel so two consumers never race for the same single
// channel — the docstring explicitly warns about this and
// neither path was previously exercised under test.
func TestHandleFileEventsAndSubscribeFileEventsAreSiblings(t *testing.T) {
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

	// Build a tiny torrent on disk; AddTorrentFile gives us a
	// real Handle whose fileSub is wired up.
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.bin")
	body := make([]byte, 32*1024)
	for i := range body {
		body[i] = byte('a' + i%26)
	}
	if err := os.WriteFile(srcPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	torrentPath := filepath.Join(dir, "x.torrent")
	if _, _, err := eng.CreateTorrentFile(engine.CreateTorrentOptions{Root: srcPath}, torrentPath); err != nil {
		t.Fatalf("CreateTorrentFile: %v", err)
	}
	h, err := eng.AddTorrentFile(torrentPath)
	if err != nil {
		t.Fatalf("AddTorrentFile: %v", err)
	}

	// Both methods must return non-nil channels.
	chFile := h.FileEvents()
	chSub := h.SubscribeFileEvents()
	if chFile == nil {
		t.Error("FileEvents() returned nil channel")
	}
	if chSub == nil {
		t.Error("SubscribeFileEvents() returned nil channel")
	}
	// They must be different channels: per-call subscriptions
	// fan out independently. If FileEvents and SubscribeFileEvents
	// silently aliased to the same channel, two consumers would
	// race for the same buffer and one would always lose.
	if chFile == chSub {
		t.Error("FileEvents and SubscribeFileEvents returned the same channel (must be distinct subscriptions)")
	}
}
