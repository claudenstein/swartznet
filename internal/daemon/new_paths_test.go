package daemon_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/daemon"
)

// TestDaemonNewBadAPIAddrIsNonFatal verifies that an unbindable
// APIAddr is logged to Stderr but does not fail daemon.New — the
// daemon still comes up, just with d.API == nil.
func TestDaemonNewBadAPIAddrIsNonFatal(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.IndexDir = t.TempDir()
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true

	var stderr bytes.Buffer
	d, err := daemon.New(context.Background(), daemon.Options{
		Cfg:     cfg,
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		APIAddr: "invalid-host:not-a-port", // unparseable -> bind fails
		Version: "test",
		Stderr:  &stderr,
	})
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}
	defer d.Close()

	if d.API != nil {
		t.Error("d.API should be nil when bind fails")
	}
	if !strings.Contains(stderr.String(), "httpapi") {
		t.Errorf("stderr should mention httpapi failure, got %q", stderr.String())
	}
}

// TestDaemonNewIndexDirIsAFile exercises the indexer.Open failure
// path. We hand New an IndexDir that points to an existing regular
// file, so Bleve cannot open or create an index there; New returns
// the error and tears the engine down.
func TestDaemonNewIndexDirIsAFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.IndexDir = blocker
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true

	_, err := daemon.New(context.Background(), daemon.Options{
		Cfg:     cfg,
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Version: "test",
	})
	if err == nil {
		t.Fatal("daemon.New should fail when IndexDir is a regular file")
	}
}

// TestDaemonNewDataDirInvalid covers the engine.New failure path.
// engine.New rejects an empty DataDir during config validation, so
// daemon.New must propagate the error rather than panicking.
func TestDaemonNewDataDirInvalid(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.DataDir = "" // engine.New / config.Validate rejects this
	cfg.IndexDir = t.TempDir()
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true

	_, err := daemon.New(context.Background(), daemon.Options{
		Cfg:     cfg,
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Version: "test",
	})
	if err == nil {
		t.Fatal("daemon.New should fail when engine.New rejects the config")
	}
}
