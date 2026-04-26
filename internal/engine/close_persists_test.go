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

// TestEngineClosePersistsBloomAndTracker covers the
// `if e.bloom != nil { e.bloom.Save() }` and matching tracker
// arms of Close. Construct an engine with valid BloomPath +
// ReputationPath, close it, then verify the files are present
// on disk afterwards.
func TestEngineClosePersistsBloomAndTracker(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	bloomPath := filepath.Join(dataDir, "bloom.bin")
	repPath := filepath.Join(dataDir, "rep.json")

	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true
	cfg.IdentityPath = ""
	cfg.SeedListPath = ""
	cfg.TrustPath = ""
	// BloomPath + ReputationPath set so engine.New wires them.
	cfg.BloomPath = bloomPath
	cfg.ReputationPath = repPath

	eng, err := engine.New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(bloomPath); err != nil {
		t.Errorf("bloom file missing after Close: %v", err)
	}
	if _, err := os.Stat(repPath); err != nil {
		t.Errorf("reputation file missing after Close: %v", err)
	}
}
