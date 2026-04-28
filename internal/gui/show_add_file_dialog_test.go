package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestShowAddFileDialogConstructs covers showAddFileDialog at
// downloads.go:615-639. The function builds a NewFileOpen dialog
// and shows it; the OnSelected callback (which calls
// dl.d.Eng.AddTorrentFile) only fires when the user picks a file,
// so just constructing the dialog stays daemon-free.
func TestShowAddFileDialogConstructs(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	dl := &downloadsTab{
		content: widget.NewLabel("downloads"),
	}
	dl.showAddFileDialog()
}
