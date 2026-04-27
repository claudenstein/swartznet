package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestCreateTorrentDialogConstructs covers createTorrentDialog
// at create.go:55-221. The dialog builder wires up rootEntry,
// nameEntry, autofillName, browse buttons, and a confirm
// callback. None of these touch the daemon during construction
// — d is only dereferenced inside the OnConfirm callback (which
// we don't trigger here). We can pass a nil daemon as long as
// the confirm path stays unexecuted.
func TestCreateTorrentDialogConstructs(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	createTorrentDialog(nil, w)
}
