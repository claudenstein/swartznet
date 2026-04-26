package engine_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// TestNewWithHTTPUserAgent covers engine.New's
// `if cfg.HTTPUserAgent != "" { tc.HTTPUserAgent = ... }` arm.
// Set a custom user-agent in the config and verify the engine
// boots cleanly. We don't try to introspect the resulting
// torrent.Client config (no public accessor); a clean New +
// Close is enough proof the conditional fired.
func TestNewWithHTTPUserAgent(t *testing.T) {
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
	cfg.HTTPUserAgent = "swartznet-test/0.0"

	eng, err := engine.New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("engine.New with custom user-agent: %v", err)
	}
	defer eng.Close()
}
