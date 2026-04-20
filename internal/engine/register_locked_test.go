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

// TestRegisterLockedDedupsExistingHandle covers the previously-
// uncovered dup-add fast-return in registerLocked. AddMagnet
// twice on the same infohash must yield the same Handle.
func TestRegisterLockedDedupsExistingHandle(t *testing.T) {
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
	hexIH := hex.EncodeToString(ih[:])
	magnet := "magnet:?xt=urn:btih:" + hexIH

	first, err := eng.AddMagnet(magnet)
	if err != nil {
		t.Fatalf("first AddMagnet: %v", err)
	}
	second, err := eng.AddMagnet(magnet)
	if err != nil {
		t.Fatalf("second AddMagnet: %v", err)
	}

	if first != second {
		t.Errorf("re-adding same magnet returned a different handle (first=%p second=%p)",
			first, second)
	}
	// Engine should still report exactly one torrent.
	if got := len(eng.Torrents()); got != 1 {
		t.Errorf("Torrents() len after dup-add = %d, want 1", got)
	}
}
