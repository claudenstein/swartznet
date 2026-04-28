package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/engine"
)

// TestFilesDialogBulkButtons covers the "Select All" /
// "Deselect All" button OnTapped closures inside
// filesDialog.build. After build()+show() the dialog content
// is added to the canvas overlay stack — we walk the canvas
// to find each button by label and invoke OnTapped.
func TestFilesDialogBulkButtons(t *testing.T) {
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
	fd.build("test")

	// Walk every laid-out child of every overlay on the canvas
	// (the dialog's content is added as an overlay). LaidOutObjects
	// returns descendants in fully-laid-out form including widgets
	// that wrap their content (PopUps, Cards, etc.).
	overlays := w.Canvas().Overlays().List()
	for _, ov := range overlays {
		for _, child := range test.LaidOutObjects(ov) {
			if btn, ok := child.(*widget.Button); ok && btn.OnTapped != nil {
				switch btn.Text {
				case "Select All", "Deselect All":
					btn.OnTapped()
				}
			}
		}
	}
	fd.dlg.Hide()
}
