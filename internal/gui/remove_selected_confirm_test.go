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

	// Tapping "Yes" spawns removeSelected's goroutine, which ends in
	// fyne.Do. Join it deterministically via the afterRemoveSelected
	// seam instead of sleeping.
	done := make(chan struct{}, 1)
	afterRemoveSelected = func() {
		select {
		case done <- struct{}{}:
		default:
		}
	}
	t.Cleanup(func() { afterRemoveSelected = nil })

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
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("removeSelected goroutine did not complete")
	}
}

// TestRemoveSelectedConfirmCanceled covers the `if !ok { return }`
// arm of removeSelected's confirm callback. Tapping "No" yields
// ok=false; the goroutine never fires and no engine work runs.
func TestRemoveSelectedConfirmCanceled(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	dl := &downloadsTab{
		d:        d,
		content:  widget.NewLabel("downloads"),
		selected: 0,
		snaps: []engine.TorrentSnapshot{
			{InfoHash: "0123456789abcdef0123456789abcdef01234567", Name: "test"},
		},
	}
	dl.removeSelected()

	for _, ov := range w.Canvas().Overlays().List() {
		for _, child := range test.LaidOutObjects(ov) {
			if btn, ok := child.(*widget.Button); ok && btn.Text == "No" && btn.OnTapped != nil {
				btn.OnTapped()
			}
		}
	}
	// No goroutine spawned on cancel; brief drain just for any
	// dialog rendering to settle.
	time.Sleep(100 * time.Millisecond)
}
