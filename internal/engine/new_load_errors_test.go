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

// TestNewLogsBloomLoadError covers engine.New's bloom-load
// `if err != nil { log.Warn(...) }` arm. Point cfg.BloomPath at
// a directory so reputation.LoadOrCreateBloom fails reading it;
// engine.New must continue (load is non-fatal) and the engine
// should come up with eng.bloom == nil. The test checks the
// no-op happy path rather than parsing log output.
func TestNewLogsBloomLoadError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bloomDir := filepath.Join(dir, "bloom-as-directory")
	if err := os.Mkdir(bloomDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bloomDir, "blocker"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true
	cfg.IdentityPath = ""
	cfg.ReputationPath = ""
	cfg.SeedListPath = ""
	cfg.BloomPath = bloomDir // directory → ReadFile error
	cfg.TrustPath = ""

	eng, err := engine.New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("engine.New: %v (bloom load failure should be non-fatal)", err)
	}
	defer eng.Close()
}

// TestNewLogsReputationLoadError covers engine.New's reputation-
// load `if err != nil { log.Warn(...) }` arm. Same pattern as
// TestNewLogsBloomLoadError but pointed at the reputation path.
func TestNewLogsReputationLoadError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	repDir := filepath.Join(dir, "reputation-as-directory")
	if err := os.Mkdir(repDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repDir, "blocker"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true
	cfg.IdentityPath = ""
	cfg.ReputationPath = repDir
	cfg.SeedListPath = ""
	cfg.BloomPath = ""
	cfg.TrustPath = ""

	eng, err := engine.New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("engine.New: %v (reputation load failure should be non-fatal)", err)
	}
	defer eng.Close()
}
