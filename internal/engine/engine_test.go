package engine_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// TestEngineStartStop verifies that the Engine wrapper can bring up a
// torrent.Client with DHT and uploads disabled (so we don't touch the real
// network in CI) and then shut it down cleanly. This is the minimal M1
// smoke test for the wrapper code path.
func TestEngineStartStop(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.ListenPort = 0 // OS-assigned, avoids port collisions in parallel tests
	cfg.DisableDHT = true
	cfg.NoUpload = true

	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	eng, err := engine.New(ctx, cfg, log)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}

	if port := eng.LocalPort(); port == 0 {
		t.Errorf("LocalPort = 0, want an OS-assigned nonzero port")
	}

	if got := len(eng.Torrents()); got != 0 {
		t.Errorf("Torrents() len = %d, want 0", got)
	}

	if err := eng.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	// Close is required to be idempotent.
	if err := eng.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// TestEngineConfigValidation ensures that a bad Config is rejected before
// we try to start the underlying Client.
func TestEngineConfigValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		mut  func(*config.Config)
	}{
		{"empty data dir", func(c *config.Config) { c.DataDir = "" }},
		{"negative port", func(c *config.Config) { c.ListenPort = -1 }},
		{"port too high", func(c *config.Config) { c.ListenPort = 70000 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.DataDir = t.TempDir()
			cfg.DisableDHT = true
			tc.mut(&cfg)
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			if _, err := engine.New(context.Background(), cfg, log); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}
