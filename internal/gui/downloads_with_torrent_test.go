package gui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/engine"
)

// addTestTorrent creates a small file, packages it as a torrent
// via the engine, adds the metainfo, and returns the resulting
// info-hash hex. Callers use this when they need a downloadsTab
// that has a real torrent for pauseSelected / resumeSelected /
// toggleIndexSelected / showFilesForSelected coverage.
func addTestTorrent(t *testing.T, eng *engine.Engine) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(src, []byte("test payload for gui downloads tests"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "test.torrent")
	ih, mi, err := eng.CreateTorrentFile(engine.CreateTorrentOptions{
		Root: src,
		Name: "payload.bin",
	}, out)
	if err != nil {
		t.Fatalf("CreateTorrentFile: %v", err)
	}
	if _, err := eng.AddTorrentMetaInfo(mi); err != nil {
		t.Fatalf("AddTorrentMetaInfo: %v", err)
	}
	// Snapshots may need a moment to populate.
	for i := 0; i < 20; i++ {
		snaps := eng.TorrentSnapshots()
		for _, s := range snaps {
			if s.InfoHash == ih {
				return ih
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("torrent %s never appeared in TorrentSnapshots", ih)
	return ""
}

// TestDownloadsSelectedActionsHappyPath covers pauseSelected,
// resumeSelected, removeSelected, toggleIndexSelected, and
// showFilesForSelected against a real daemon with one real
// torrent in flight. Each action's daemon-touching arm fires.
func TestDownloadsSelectedActionsHappyPath(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)
	ih := addTestTorrent(t, d.Eng)

	dl := &downloadsTab{
		d:        d,
		content:  widget.NewLabel("downloads"),
		selected: 0,
		snaps: []engine.TorrentSnapshot{
			{InfoHash: ih, Name: "payload.bin", Indexing: true},
		},
	}

	// Each go-routine launches against the real engine. They
	// return without panicking.
	dl.pauseSelected()
	dl.resumeSelected()
	dl.toggleIndexSelected()
	dl.showFilesForSelected()
	// showFilesForSelected builds a filesDialog that spawns a
	// 2 s-tick pollLoop. Tap the "Close" button to fire
	// SetOnClosed → cancel(), so the goroutine exits before
	// next test starts.
	for _, ov := range w.Canvas().Overlays().List() {
		for _, child := range test.LaidOutObjects(ov) {
			if btn, ok := child.(*widget.Button); ok && btn.Text == "Close" && btn.OnTapped != nil {
				btn.OnTapped()
			}
		}
	}
	// Wait so the per-action goroutines + pollLoop fully drain.
	time.Sleep(300 * time.Millisecond)
}

// TestShowSignatureDialogSigned covers showSignatureDialog at
// downloads.go:351-380 when SignedBy is non-empty. With a real
// daemon the trustStore is non-nil so the Label() call hits the
// non-nil arm; the dialog renders the trusted/untrusted labels.
func TestShowSignatureDialogSigned(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	dl := &downloadsTab{
		d:       d,
		content: widget.NewLabel("downloads"),
	}

	// Untrusted signature.
	dl.showSignatureDialog(engine.TorrentSnapshot{
		Name:             "untrusted",
		InfoHash:         "0123456789abcdef0123456789abcdef01234567",
		SignedBy:         "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
		TrustedPublisher: false,
	})
	// Trusted signature — exercises the bold-label arm.
	dl.showSignatureDialog(engine.TorrentSnapshot{
		Name:             "trusted",
		InfoHash:         "0123456789abcdef0123456789abcdef01234567",
		SignedBy:         "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
		TrustedPublisher: true,
	})
}
