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

// TestRemoveTorrentUnknownInfoHashErrors covers RemoveTorrent's
// `h, err := handleByHex(...); if err != nil { return err }`
// arm when the infohash isn't tracked. Verifies the call
// surfaces an error rather than panicking on a nil handle.
func TestRemoveTorrentUnknownInfoHashErrors(t *testing.T) {
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

	var ih [20]byte
	if _, err := rand.Read(ih[:]); err != nil {
		t.Fatal(err)
	}
	if err := eng.RemoveTorrent(hex.EncodeToString(ih[:])); err == nil {
		t.Error("RemoveTorrent on unknown infohash should error")
	}
}
