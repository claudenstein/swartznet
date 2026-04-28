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

// TestDownloadsPollLoopTickEmpty covers the if branch of
// pollLoop's empty-state toggle: with no torrents, snaps stays
// empty and emptyState.Show() fires.
func TestDownloadsPollLoopTickEmpty(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor-empty")
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
	time.Sleep(2400 * time.Millisecond)
	cancel()
	time.Sleep(50 * time.Millisecond)
}

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
	// Add a torrent so the pollLoop tick.C body sees non-empty
	// snaps and takes the else (Hide empty-state) arm.
	addTestTorrent(t, d.Eng)

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
	// Wait for one tick to fire (2s cadence) plus margin for the
	// fyne.Do callback to drain on the test thread.
	time.Sleep(2400 * time.Millisecond)
	cancel()
	time.Sleep(50 * time.Millisecond)
}
