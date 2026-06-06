package daemon

import (
	"context"
	"encoding/hex"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/config"
)

// TestCloseCancelsAnchorBootstrap is a regression guard for the
// concurrency finding: the anchor-fetch goroutine spawned in New
// was neither cancel-tracked nor awaited, so Close (which takes no
// ctx) relied on the caller cancelling the parent ctx. Now New
// derives a Daemon-owned child context and joins the goroutine via a
// WaitGroup, so Close deterministically stops the background fetch
// even while the parent ctx stays alive.
//
// White-box (package daemon) so it can seed DefaultAnchorPubkeys —
// daemon.Options has no public hook for injecting anchors. Uses the
// in-process regtest DHT; the anchor key is never published, so the
// GetPPMI traversal blocks until Close cancels the bg context.
func TestCloseCancelsAnchorBootstrap(t *testing.T) {
	// Seed one anchor so New spawns the RunAnchors goroutine.
	var anchor [32]byte
	for i := range anchor {
		anchor[i] = byte(i + 1)
	}
	prev := DefaultAnchorPubkeys
	DefaultAnchorPubkeys = []string{hex.EncodeToString(anchor[:])}
	t.Cleanup(func() { DefaultAnchorPubkeys = prev })

	dir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = dir
	cfg.IndexDir = t.TempDir()
	cfg.CompanionDir = ""        // keep companion subsystems out of the picture
	cfg.CompanionFollowFile = "" //
	cfg.ListenPort = 0
	cfg.DisableDHT = false
	cfg.NoUpload = true
	cfg.IdentityPath = filepath.Join(dir, "identity.key")
	cfg.ReputationPath = ""
	cfg.SeedListPath = ""
	cfg.BloomPath = ""
	cfg.TrustPath = ""
	cfg.Regtest = true

	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Parent ctx deliberately stays alive for the whole test — the
	// point is that Close stops the bg work WITHOUT relying on it.
	parent := context.Background()
	d, err := New(parent, Options{Cfg: cfg, Log: log})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if d.Bootstrap == nil {
		t.Fatal("Bootstrap should attach with regtest DHT")
	}
	if len(d.Bootstrap.AnchorKeys()) != 1 {
		t.Fatalf("expected 1 seeded anchor, got %d", len(d.Bootstrap.AnchorKeys()))
	}

	// Close must return promptly: bgCancel cancels the in-flight
	// GetPPMI traversal and bgWG.Wait joins the goroutine before the
	// engine is torn down. A hang here means the goroutine isn't
	// cancel-tracked.
	done := make(chan error, 1)
	go func() { done <- d.Close() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Close did not return promptly — background anchor fetch was not cancelled/joined")
	}
}
