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

// newTestDaemon spins up a minimal daemon with no DHT, no upload,
// no identity / reputation / bloom / trust persistence — just
// enough wiring for gui constructors and refresh helpers to call
// d.Eng / d.Index getters without segfaulting. Caller defers
// d.Close().
func newTestDaemon(t *testing.T) *daemon.Daemon {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.IndexDir = t.TempDir()
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true
	cfg.IdentityPath = ""
	// Setting BloomPath + ReputationPath under TempDir makes the
	// engine construct non-nil KnownGoodBloom + ReputationTracker
	// so confirmHit / flagHit tests can exercise the daemon arms.
	root := t.TempDir()
	cfg.BloomPath = filepath.Join(root, "bloom.dat")
	cfg.ReputationPath = filepath.Join(root, "reputation.json")
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
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// TestNewStatusTabAndRefresh covers newStatusTab construction and
// statusTab.refresh against a real (minimal) daemon. The
// constructor builds every status card and starts pollLoop —
// passing a canceled context makes pollLoop's body run once
// (refresh) and then return on the for-select's ctx.Done arm.
func TestNewStatusTabAndRefresh(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pollLoop runs initial refresh, then returns

	st := newStatusTab(ctx, d)
	if st == nil {
		t.Fatal("expected non-nil statusTab")
	}
	if st.content == nil {
		t.Error("expected content wired up")
	}

	// Drive refresh once more directly so the function gets full
	// in-test coverage rather than just the goroutine pre-cancel
	// snapshot.
	st.refresh()
}

// TestNewSettingsTabAndApply covers newSettingsTab construction +
// loadCurrent + loadRateLimits + loadQueueSettings + applyRateLimits
// + applyQueueSettings + save against a real daemon. Each of those
// helpers reads (and one writes back) engine state via d.Eng or
// SwarmSearch, so a real daemon is required.
func TestNewSettingsTabAndApply(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	st := newSettingsTab(d)
	if st == nil {
		t.Fatal("expected non-nil settingsTab")
	}

	// Apply a clean upload+download value through the rate-limit
	// path. parseKiB succeeds, so this exercises the daemon-write
	// arm and the dialog.ShowInformation success arm.
	st.uploadEntry.SetText("100")
	st.downloadEntry.SetText("200")
	st.applyRateLimits()

	// Apply a queue setting through the success arm.
	st.maxActiveEntry.SetText("3")
	st.applyQueueSettings()

	// Save sharing capabilities — touches SwarmSearch.SetCapabilities.
	st.save()
}
