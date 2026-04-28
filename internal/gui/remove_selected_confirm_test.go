package gui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/engine"
)

// TestRemoveSelectedConfirmAction taps the "Remove" button on
// the confirm dialog spawned by removeSelected so the
// `if !ok { return }` path is bypassed and the goroutine
// actually invokes RemoveTorrent + flips selected back to -1.
func TestRemoveSelectedConfirmAction(t *testing.T) {
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
			{InfoHash: ih, Name: "test"},
		},
	}
	dl.removeSelected()

	// Find the confirm dialog's "Remove" button on the canvas
	// overlay stack and tap it.
	for _, ov := range w.Canvas().Overlays().List() {
		for _, child := range test.LaidOutObjects(ov) {
			btn, ok := child.(*widget.Button)
			if !ok || btn.OnTapped == nil {
				continue
			}
			if btn.Text == "Yes" {
				btn.OnTapped()
			}
		}
	}
	// Wait for the confirmation goroutine to fire RemoveTorrent.
	time.Sleep(150 * time.Millisecond)
}
