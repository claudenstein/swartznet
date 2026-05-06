package gui

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/daemon"
)

// TestShowAboutWithAPI covers showAbout's `a.daemon.API != nil`
// arm at app.go:289-291. We spin up a daemon with APIAddr set
// to a localhost ephemeral port so the HTTP API binds and
// d.API is non-nil.
func TestShowAboutWithAPI(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	root := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = filepath.Join(root, "data")
	cfg.IndexDir = filepath.Join(root, "index")
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true
	cfg.IdentityPath = filepath.Join(root, "identity.key")
	cfg.BloomPath = filepath.Join(root, "bloom.dat")
	cfg.ReputationPath = filepath.Join(root, "reputation.json")
	cfg.SeedListPath = ""
	cfg.TrustPath = ""

	d, err := daemon.New(context.Background(), daemon.Options{
		Cfg:     cfg,
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		APIAddr: "127.0.0.1:0", // OS-assigned ephemeral port
		Version: "test",
	})
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}
	defer d.Close()
	if d.API == nil {
		skipMissing(t, d, "API")
		return
	}

	a := &App{daemon: d, win: w, version: "test"}
	a.showAbout()
}
