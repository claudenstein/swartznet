package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestShowAddMagnetDialogConstructs covers showAddMagnetDialog
// and showAddMagnetDialogPrefilled at downloads.go:489-566.
// Both build a NewForm dialog and Show() it. The submit
// callback isn't invoked here, so the daemon-touching path
// stays unexecuted — we only cover the dialog-construction arm.
func TestShowAddMagnetDialogConstructs(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	dl := &downloadsTab{
		content: widget.NewLabel("downloads"),
	}
	// Empty-prefill wrapper — exercises the one-line wrapper at
	// downloads.go:489-491.
	dl.showAddMagnetDialog()
	// Prefilled with a URI — exercises the `if prefill != "" {
	// entry.SetText(prefill) }` arm.
	dl.showAddMagnetDialogPrefilled("magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567", false)
}
