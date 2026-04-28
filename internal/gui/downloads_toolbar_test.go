package gui

import (
	"context"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestDownloadsToolbarButtons taps each toolbar button on the
// downloads tab so the OnTapped closure body fires. With
// selected=-1 the per-row actions short-circuit on
// selectedInfoHash() == "" but the closure prefix runs.
func TestDownloadsToolbarButtons(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dl := newDownloadsTab(ctx, d)

	for _, label := range []string{
		"Add Magnet",
		"Add .torrent",
		"Create Torrent",
		"Pause",
		"Resume",
		"Remove",
		"Toggle Index",
		"Files...",
	} {
		btn := findButtonByLabel(dl.content, label)
		if btn == nil {
			t.Errorf("toolbar button %q not found", label)
			continue
		}
		if btn.OnTapped != nil {
			btn.OnTapped()
		}
	}
	// Brief wait so any goroutines spawned have a moment to fire.
	time.Sleep(50 * time.Millisecond)
}
