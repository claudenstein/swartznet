package engine_test

import (
	"context"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// TestEngineNewNilLoggerFallsBack covers the
// `log == nil → slog.Default()` branch of engine.New. Every
// other test passes a non-nil logger, so the substitute was
// never exercised.
func TestEngineNewNilLoggerFallsBack(t *testing.T) {
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

	eng, err := engine.New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("engine.New(nil log): %v", err)
	}
	defer eng.Close()
	if eng == nil {
		t.Error("engine.New(nil log) returned nil engine")
	}
}
