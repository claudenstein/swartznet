package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestCreateTorrentDialogRootEntryAutofill drives rootEntry's
// OnChanged hook so the autofillName closure runs. Multiple
// SetText calls cover the early-return arms (empty/dot/slash
// base) and the nameEntry-text-replacement arm.
func TestCreateTorrentDialogRootEntryAutofill(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	createTorrentDialog(nil, w)

	// Find the rootEntry by placeholder text.
	var rootEntry *widget.Entry
	for _, ov := range w.Canvas().Overlays().List() {
		for _, child := range test.LaidOutObjects(ov) {
			e, ok := child.(*widget.Entry)
			if !ok {
				continue
			}
			if e.PlaceHolder == "/path/to/file-or-folder" {
				rootEntry = e
				break
			}
		}
		if rootEntry != nil {
			break
		}
	}
	if rootEntry == nil {
		t.Fatal("rootEntry not found")
	}

	// Empty base — autofillName returns early.
	rootEntry.SetText("/")
	// Valid base — first call fills nameEntry.
	rootEntry.SetText("/some/path/file.bin")
	// Same base again — nameEntry.Text now equals lastAutofill, so
	// the function re-fills it.
	rootEntry.SetText("/another/path/file.bin")
}
