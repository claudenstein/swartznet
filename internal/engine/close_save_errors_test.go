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

// TestEngineCloseTolerateBloomSaveError covers Close's
// `if err := e.bloom.Save(); err != nil { log.Warn(...) }` arm.
// Strip write permission on the bloom file's parent directory
// after engine.New has created the bloom and stamp time so the
// Save during Close fails. Close must still succeed.
//
// Skipped on Windows (chmod) and as root (privileged write).
func TestEngineCloseTolerateBloomSaveError(t *testing.T) {
	t.Parallel()
	if os.Getuid() == 0 {
		t.Skip("running as root, chmod 0o500 doesn't deny writes")
	}
	dataDir := t.TempDir()
	bloomDir := filepath.Join(dataDir, "bloom-state")
	if err := os.Mkdir(bloomDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bloomPath := filepath.Join(bloomDir, "bloom.bin")

	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true
	cfg.IdentityPath = ""
	cfg.ReputationPath = ""
	cfg.SeedListPath = ""
	cfg.BloomPath = bloomPath
	cfg.TrustPath = ""

	eng, err := engine.New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}

	// Now strip write permission on the bloom dir so the Save
	// inside Close fails on the tmp WriteFile.
	if err := os.Chmod(bloomDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(bloomDir, 0o755) })

	if err := eng.Close(); err != nil {
		t.Fatalf("Close: %v (bloom Save failure must be non-fatal)", err)
	}
}

// TestEngineCloseTolerateTrackerSaveError mirrors the bloom test
// for the reputation tracker's Save-error arm.
func TestEngineCloseTolerateTrackerSaveError(t *testing.T) {
	t.Parallel()
	if os.Getuid() == 0 {
		t.Skip("running as root, chmod 0o500 doesn't deny writes")
	}
	dataDir := t.TempDir()
	repDir := filepath.Join(dataDir, "rep-state")
	if err := os.Mkdir(repDir, 0o755); err != nil {
		t.Fatal(err)
	}
	repPath := filepath.Join(repDir, "rep.json")

	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true
	cfg.IdentityPath = ""
	cfg.ReputationPath = repPath
	cfg.SeedListPath = ""
	cfg.BloomPath = ""
	cfg.TrustPath = ""

	eng, err := engine.New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}

	if err := os.Chmod(repDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(repDir, 0o755) })

	if err := eng.Close(); err != nil {
		t.Fatalf("Close: %v (tracker Save failure must be non-fatal)", err)
	}
}
