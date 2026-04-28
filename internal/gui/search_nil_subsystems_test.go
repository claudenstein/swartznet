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

// newNoSubsystemsTestDaemon constructs a daemon whose engine has
// neither bloom nor reputation tracker (BloomPath +
// ReputationPath both empty), so KnownGoodBloom() and
// ReputationTracker() return nil. Used to exercise the
// nil-subsystem early-returns in confirmHit / flagHit.
func newNoSubsystemsTestDaemon(t *testing.T) *daemon.Daemon {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.IndexDir = t.TempDir()
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true
	root := t.TempDir()
	cfg.IdentityPath = filepath.Join(root, "identity.key")
	// Empty BloomPath / ReputationPath leave the engine without a
	// bloom or reputation tracker.
	cfg.BloomPath = ""
	cfg.ReputationPath = ""
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

// TestConfirmHitNilBloomShortCircuits covers confirmHit's
// `if bloom == nil { return }` arm (search.go:332-335). With a
// daemon whose BloomPath is empty, KnownGoodBloom() returns nil
// and the function returns immediately without showing a
// dialog.
func TestConfirmHitNilBloomShortCircuits(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newNoSubsystemsTestDaemon(t)
	st := &searchTab{
		d:       d,
		content: widget.NewLabel("search"),
	}
	st.confirmHit("0123456789abcdef0123456789abcdef01234567")
}

// TestFlagHitNilTrackerShortCircuits covers flagHit's
// `if tracker == nil { return }` arm (search.go:356-359). With a
// daemon whose ReputationPath is empty, ReputationTracker()
// returns nil and the function returns immediately.
func TestFlagHitNilTrackerShortCircuits(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newNoSubsystemsTestDaemon(t)
	st := &searchTab{
		d:       d,
		content: widget.NewLabel("search"),
	}
	st.flagHit("0123456789abcdef0123456789abcdef01234567")
}
