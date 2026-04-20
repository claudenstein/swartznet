package engine_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// TestResumeTorrentRestoresPausedAndIsIdempotent exercises the
// previously-uncovered branches of ResumeTorrent:
//   - resume on a paused torrent runs the AllowDataDownload/Upload +
//     persistState body
//   - resume on an already-running torrent fast-returns nil without
//     re-touching anacrolix or the session file
func TestResumeTorrentRestoresPausedAndIsIdempotent(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true
	// Keep file-backed paths empty so the test does not pull in the
	// user's XDG state.
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

	// Add a magnet so we have a real handle in the engine.
	var ih [20]byte
	if _, err := rand.Read(ih[:]); err != nil {
		t.Fatal(err)
	}
	hexIH := hex.EncodeToString(ih[:])
	magnet := "magnet:?xt=urn:btih:" + hexIH
	if _, err := eng.AddMagnet(magnet); err != nil {
		t.Fatalf("AddMagnet: %v", err)
	}

	// Pause first, then resume — covers the wasPaused=true main body.
	if err := eng.PauseTorrent(hexIH); err != nil {
		t.Fatalf("PauseTorrent: %v", err)
	}
	if err := eng.ResumeTorrent(hexIH); err != nil {
		t.Fatalf("ResumeTorrent (paused→running): %v", err)
	}

	// Second resume should fast-return nil (wasPaused=false branch).
	if err := eng.ResumeTorrent(hexIH); err != nil {
		t.Fatalf("ResumeTorrent (already running): %v", err)
	}
}
