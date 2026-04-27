package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/engine"
)

// TestShowFilesDialogUnknownInfoHash covers showFilesDialog at
// files_dialog.go:21-40 against a real daemon. With no torrents
// added the engine returns "torrent not found" for any infohash,
// so the function calls dialog.ShowError + return — the err arm.
func TestShowFilesDialogUnknownInfoHash(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)
	showFilesDialog(d, w, "0123456789abcdef0123456789abcdef01234567", "missing")
}

// TestFilesDialogBuildWithPrefilledFiles covers filesDialog.build
// at files_dialog.go:55-156. We hand-populate fd.files (the
// engine doesn't yet know about this torrent, so TorrentFiles
// would err) and call build directly. The dialog is constructed
// + shown; pollLoop spawns but its tick.C reads will err-continue.
func TestFilesDialogBuildWithPrefilledFiles(t *testing.T) {
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
			{Index: 0, DisplayPath: "a.txt", Length: 100, Progress: 0.5, Priority: "normal"},
			{Index: 1, DisplayPath: "b.txt", Length: 200, Progress: 1.0, Priority: "high"},
		},
	}
	fd.build("test torrent")

	// Closing the dialog triggers the OnClosed callback which
	// cancels the goroutine context so we don't leak.
	fd.dlg.Hide()
}
