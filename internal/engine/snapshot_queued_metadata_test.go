package engine

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
)

// TestSnapshotOfQueuedBeforeMetadata covers snapshotOf's
// `else if queued { status = "queued" }` arm at engine.go:1929-1931.
// A magnet handle without GotInfo metadata has t.Info()==nil; if
// it's also queued, the early-return Status must be "queued"
// (not "metadata"). Reachable only via the internal setQueued
// helper, since the public queueOrActivate path runs after
// autoDownload waits on GotInfo.
func TestSnapshotOfQueuedBeforeMetadata(t *testing.T) {
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

	eng, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	const magnet = "magnet:?xt=urn:btih:1234567890abcdef1234567890abcdef12345678"
	h, err := eng.AddMagnet(magnet)
	if err != nil {
		t.Fatalf("AddMagnet: %v", err)
	}
	// No metadata (DHT disabled, no peers), so t.Info() stays nil.
	// Force queued state to drive the queued-and-metadata-pending
	// branch of snapshotOf.
	h.setQueued(true)

	snap := snapshotOf(h)
	if snap.Status != "queued" {
		t.Errorf("Status = %q, want %q", snap.Status, "queued")
	}
	if !snap.Queued {
		t.Errorf("Queued = false, want true")
	}
}
