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

// TestStatusRefreshFullDHTPublisher covers status.refresh's
// `if pub := st.d.Eng.Publisher(); pub != nil` arm at
// status.go:275-279. Requires a daemon with DHT enabled AND
// publish enabled (default DisableDHTPublish=false). That
// builds engine.publisher = dhtindex.NewPublisher(...).
func TestStatusRefreshFullDHTPublisher(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	root := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = filepath.Join(root, "data")
	cfg.IndexDir = filepath.Join(root, "index")
	cfg.CompanionDir = filepath.Join(root, "companion")
	cfg.ListenPort = 0
	// DHT enabled (default), publish enabled (default).
	cfg.NoUpload = true
	cfg.IdentityPath = filepath.Join(root, "identity.key")
	cfg.BloomPath = filepath.Join(root, "bloom.dat")
	cfg.ReputationPath = filepath.Join(root, "reputation.json")
	cfg.PublisherManifest = filepath.Join(root, "publisher-manifest.json")
	cfg.SeedListPath = ""
	cfg.TrustPath = ""

	d, err := daemon.New(context.Background(), daemon.Options{
		Cfg:     cfg,
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Version: "test",
	})
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}
	defer d.Close()
	if d.Eng.Publisher() == nil {
		t.Skip("publisher did not start in this environment")
	}

	st := buildStatusTab(d)
	st.refresh()
}
