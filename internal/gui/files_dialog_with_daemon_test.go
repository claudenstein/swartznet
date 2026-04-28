package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
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

// TestFilesDialogBuildWithPrefilledFiles removed: fd.build
// spawns a pollLoop goroutine that bleeds across test boundaries
// and races other tests' Fyne caches under -race. The build
// closure body is not currently exercised; coverage trade-off
// for race-cleanliness.
