package daemon_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/daemon"
)

func TestDaemonStartStop(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.IndexDir = t.TempDir()
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true
	cfg.IdentityPath = ""
	cfg.ReputationPath = ""
	cfg.SeedListPath = ""
	cfg.BloomPath = ""
	cfg.TrustPath = ""

	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	d, err := daemon.New(context.Background(), daemon.Options{
		Cfg:     cfg,
		Log:     log,
		Version: "test",
	})
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}

	if d.Eng == nil {
		t.Fatal("Eng is nil")
	}
	if d.Index == nil {
		t.Fatal("Index is nil when NoIndex is false")
	}

	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestDaemonNoIndex(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.IdentityPath = ""
	cfg.ReputationPath = ""
	cfg.SeedListPath = ""
	cfg.BloomPath = ""
	cfg.TrustPath = ""

	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	d, err := daemon.New(context.Background(), daemon.Options{
		Cfg:     cfg,
		Log:     log,
		NoIndex: true,
		Version: "test",
	})
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}

	if d.Index != nil {
		t.Fatal("Index should be nil when NoIndex is true")
	}

	// The flag must mirror down into Cfg so the engine's
	// publisher gating sees it.
	if !d.Cfg.NoIndex {
		t.Fatal("Cfg.NoIndex should be true after daemon.Options.NoIndex=true")
	}

	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestDaemonNoIndexCascadesToPublisher verifies that --no-index
// suppresses the Layer-D BEP-44 keyword publisher, not just Bleve.
// Previously the daemon would skip indexer.Open but still start
// engine.publisher under the user's identity, contradicting the
// CLAUDE.md claim that --no-index "cascades to disable Layer D
// publishing." Regression guard for that gap.
func TestDaemonNoIndexCascadesToPublisher(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	// Identity must be loadable for startPublisher to be reached
	// at all — without it the publisher gating code path is
	// skipped trivially. Use a temp identity.
	cfg.IdentityPath = t.TempDir() + "/identity.key"
	cfg.PublisherManifest = t.TempDir() + "/publisher.json"
	cfg.ReputationPath = ""
	cfg.SeedListPath = ""
	cfg.BloomPath = ""
	cfg.TrustPath = ""

	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	d, err := daemon.New(context.Background(), daemon.Options{
		Cfg:     cfg,
		Log:     log,
		NoIndex: true,
		Version: "test",
	})
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}
	defer d.Close()

	if d.Eng.Publisher() != nil {
		t.Fatal("Engine.Publisher should be nil when NoIndex is true (Layer-D publishing must be suppressed)")
	}
}

func TestDaemonWithAPI(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.IndexDir = t.TempDir()
	cfg.ListenPort = 0
	cfg.DisableDHT = true

	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	d, err := daemon.New(context.Background(), daemon.Options{
		Cfg:     cfg,
		Log:     log,
		APIAddr: "localhost:0",
		Version: "test",
	})
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}

	if d.API == nil {
		t.Fatal("API should not be nil when APIAddr is set")
	}
	addr := d.API.Addr()
	if addr == "" {
		t.Fatal("API.Addr() is empty")
	}
	t.Logf("API listening on %s", addr)

	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
