package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestCreateTorrentDialogButtons taps the Choose File, Choose
// Folder, and Save As buttons inside the create-torrent dialog
// so each OnTapped closure runs (which constructs and shows a
// file dialog whose callback only fires on user pick — those
// remain uncovered).
func TestCreateTorrentDialogButtons(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	createTorrentDialog(nil, w)

	// Walk the canvas overlays to find the dialog's buttons.
	for _, ov := range w.Canvas().Overlays().List() {
		for _, child := range test.LaidOutObjects(ov) {
			btn, ok := child.(*widget.Button)
			if !ok || btn.OnTapped == nil {
				continue
			}
			switch btn.Text {
			case "Choose File...", "Choose Folder...", "Save As...":
				btn.OnTapped()
			}
		}
	}
}
