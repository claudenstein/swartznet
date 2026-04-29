package gui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/engine"
)

// TestFilesDialogTickRefreshErr covers tickRefresh's err arm
// (TorrentFiles returns "torrent not found" for an unknown ih,
// the function returns without firing fyne.Do).
func TestFilesDialogTickRefreshErr(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	fd := &filesDialog{
		d:           d,
		win:         w,
		infoHashHex: "0123456789abcdef0123456789abcdef01234567",
		files: []engine.FileSnapshot{
			{Index: 0, DisplayPath: "a.txt"},
		},
	}
	fd.build("test torrent")
	defer fd.dlg.Hide()

	// Calling tickRefresh on a torrent that doesn't exist makes
	// TorrentFiles return err and tickRefresh returns without
	// touching fd.files or fd.list.Refresh.
	fd.tickRefresh()
	time.Sleep(100 * time.Millisecond)
}

// TestFilesDialogTickRefreshSuccess covers tickRefresh's
// happy-path fyne.Do(...) body — files fetch returns OK and
// the function reorders fd.files and refreshes the list.
func TestFilesDialogTickRefreshSuccess(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)
	ih := addTestTorrent(t, d.Eng)

	fd := &filesDialog{
		d:           d,
		win:         w,
		infoHashHex: ih,
		files: []engine.FileSnapshot{
			{Index: 0, DisplayPath: "a.txt"},
		},
	}
	fd.build("test torrent")
	defer fd.dlg.Hide()

	fd.tickRefresh()
	// fyne.Do schedules the closure for the UI goroutine —
	// 200 ms is plenty for the test driver to drain it.
	time.Sleep(200 * time.Millisecond)
}

// TestFilesDialogPollLoopTickFires covers pollLoop's
// `case <-tick.C: fd.tickRefresh()` arm by running the goroutine
// for slightly more than the 2s ticker interval. The test
// torrent doesn't exist so tickRefresh's err path fires (no
// fyne.Do bleed) — we only need the case branch to be reached.
func TestFilesDialogPollLoopTickFires(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	fd := &filesDialog{
		d:           d,
		win:         w,
		infoHashHex: "0123456789abcdef0123456789abcdef01234567",
		files: []engine.FileSnapshot{
			{Index: 0, DisplayPath: "a.txt"},
		},
	}
	fd.build("test torrent")

	// Ticker fires every 2s. Wait 2.3s so at least one tick
	// fires and tickRefresh runs (returns early on missing
	// torrent), then Hide+drain to let pollLoop exit.
	time.Sleep(2300 * time.Millisecond)
	fd.dlg.Hide()
	time.Sleep(100 * time.Millisecond)
}
