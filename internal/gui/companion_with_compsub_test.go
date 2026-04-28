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

// newDHTTestDaemon spins up a daemon with DHT enabled (and an
// identity + companion-dir set) so the companion subscriber +
// publisher subsystems are wired in. Required for tests that
// exercise companionTab.doFollow / unfollowAt's CompSub-touching
// arms. Heavier than newTestDaemon, so use only when needed.
func newDHTTestDaemon(t *testing.T) *daemon.Daemon {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = filepath.Join(root, "data")
	cfg.IndexDir = filepath.Join(root, "index")
	cfg.CompanionDir = filepath.Join(root, "companion")
	cfg.ListenPort = 0
	// DisableDHT defaults to false, so the engine joins mainline.
	// We set DisableDHTPublish to keep the publisher quiet.
	cfg.DisableDHTPublish = true
	cfg.NoUpload = true
	cfg.IdentityPath = filepath.Join(root, "identity.key")
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

// TestCompanionDoFollowAndUnfollow covers companionTab.doFollow at
// companion.go:234-252 and unfollowAt at companion.go:254-266 when
// CompSub is wired up. doFollow with a valid 64-char hex pubkey
// reaches CompSub.Follow; unfollowAt with a valid follow row
// reaches CompSub.Unfollow. Requires DHT + companion-dir, hence
// newDHTTestDaemon.
func TestCompanionDoFollowAndUnfollow(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newDHTTestDaemon(t)
	if d.CompSub == nil {
		t.Skip("daemon did not wire up CompSub (DHT may not be available in this environment)")
	}

	ct := &companionTab{
		d:       d,
		content: widget.NewLabel("companion"),
		follows: []followRow{
			{pubkey: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"},
		},
	}

	// doFollow happy path: 64-char hex pubkey decodes, CompSub.Follow runs.
	ct.doFollow("abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", "test-publisher")

	// doFollow `len(pubkeyHex) != 64` ShowError arm — daemon
	// has CompSub so we get past the first guard, but length
	// mismatch trips the second.
	ct.doFollow("too-short", "label")

	// doFollow hex.DecodeString-err arm — 64-char string but
	// contains non-hex characters.
	ct.doFollow("ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ", "label")

	// unfollowAt happy path: follow row 0 has a valid 64-char hex
	// pubkey, hex.DecodeString succeeds, CompSub.Unfollow runs.
	ct.unfollowAt(0)

	// unfollowAt hex.DecodeString-err arm: CompSub non-nil but
	// follow row has a non-hex pubkey, so DecodeString fails and
	// the function returns silently before calling Unfollow.
	ct.follows = append(ct.follows, followRow{pubkey: "ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ"})
	ct.unfollowAt(len(ct.follows) - 1)
}

// TestCompanionRefreshPublisherWithDaemon covers refreshPublisher
// at companion.go:220-232 when CompPub is wired up. The function
// spawns a goroutine that calls CompPub.RefreshNow; without any
// indexed content the call returns nil error so the ShowError
// branch isn't hit, but the entire happy path executes.
func TestCompanionRefreshPublisherWithDaemon(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newDHTTestDaemon(t)
	if d.CompPub == nil {
		t.Skip("daemon did not wire up CompPub (DHT may not be available in this environment)")
	}

	ct := &companionTab{
		d:       d,
		content: widget.NewLabel("companion"),
	}
	ct.refreshPublisher()
}
