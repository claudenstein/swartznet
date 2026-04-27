package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestShowAddMagnetErrorOpensDialog covers showAddMagnetError —
// it constructs the failure info dialog and shows it. The
// SetOnClosed callback is wired but never fires in this test
// (we don't close the dialog), so the showAddMagnetDialogPrefilled
// re-open isn't invoked.
func TestShowAddMagnetErrorOpensDialog(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	dl := &downloadsTab{
		content: widget.NewLabel("downloads"),
	}
	showAddMagnetError(dl, "test failure msg", "magnet:?xt=urn:btih:1234", true)
	// No assertion on dialog state — we only care that the call
	// returns without panicking and exercises the dialog.New +
	// SetOnClosed + Show path.
}
