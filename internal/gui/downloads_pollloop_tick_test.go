package gui

import (
	"context"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestDownloadsPollLoopTick covers downloads.pollLoop's tick.C
// arm at downloads.go:389-403 against a real daemon. The arm
// fetches TorrentSnapshots, sorts them under lock, refreshes
// the table, and toggles the empty-state overlay based on
// snapshot count. We construct a minimal downloadsTab with a
// real engine, run pollLoop for ~2.5s (long enough for one
// tick at 2s cadence), then cancel.
func TestDownloadsPollLoopTick(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	dl := &downloadsTab{
		d:        d,
		selected: -1,
		sortCol:  -1,
		table: widget.NewTable(
			func() (int, int) { return 0, 0 },
			func() fyne.CanvasObject { return widget.NewLabel("") },
			func(widget.TableCellID, fyne.CanvasObject) {},
		),
		emptyState: container.NewCenter(widget.NewLabel("empty")),
	}

	ctx, cancel := context.WithCancel(context.Background())
	go dl.pollLoop(ctx)
	// Wait for one tick to fire (2s cadence) plus a small margin.
	time.Sleep(2200 * time.Millisecond)
	cancel()
}
