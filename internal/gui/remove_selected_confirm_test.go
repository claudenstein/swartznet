package gui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/engine"
)

// TestRemoveSelectedConfirmAction taps the "Yes" button on
// removeSelected's confirm dialog so the !ok early-return is
// bypassed and the goroutine runs RemoveTorrent + flips selected
// back to -1. A 500 ms drain at end of test bounds the goroutine.
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

	for _, ov := range w.Canvas().Overlays().List() {
		for _, child := range test.LaidOutObjects(ov) {
			if btn, ok := child.(*widget.Button); ok && btn.Text == "Yes" && btn.OnTapped != nil {
				btn.OnTapped()
			}
		}
	}
	time.Sleep(500 * time.Millisecond)
}
