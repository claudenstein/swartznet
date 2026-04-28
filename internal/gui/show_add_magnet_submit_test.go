package gui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestShowAddMagnetDialogPrefilledSubmit covers the submit
// callback closure in showAddMagnetDialogPrefilled. With the
// dialog rendered via overlay we locate the URI Entry, set
// various texts, and tap the "Add" button to drive each arm:
// empty-URI, validateMagnetURI rejection, and engine
// AddMagnetURI happy/err.
func TestShowAddMagnetDialogPrefilledSubmit(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)
	dl := &downloadsTab{
		d:       d,
		content: widget.NewLabel("downloads"),
	}

	// Helper to find the dialog's URI entry + Add button on the
	// canvas overlay stack.
	findEntryAndAdd := func() (*widget.Entry, *widget.Button) {
		var entry *widget.Entry
		var addBtn *widget.Button
		for _, ov := range w.Canvas().Overlays().List() {
			for _, child := range test.LaidOutObjects(ov) {
				switch x := child.(type) {
				case *widget.Entry:
					if x.PlaceHolder == "magnet:?xt=urn:btih:..." {
						entry = x
					}
				case *widget.Button:
					if x.Text == "Add" {
						addBtn = x
					}
				}
			}
		}
		return entry, addBtn
	}

	// Empty-URI arm: Add with empty entry → showAddMagnetError.
	dl.showAddMagnetDialog()
	if e, b := findEntryAndAdd(); e != nil && b != nil {
		e.SetText("")
		b.OnTapped()
	}

	// validateMagnetURI rejection arm: bad scheme.
	dl.showAddMagnetDialog()
	if e, b := findEntryAndAdd(); e != nil && b != nil {
		e.SetText("http://not-a-magnet")
		b.OnTapped()
	}

	// engine.AddMagnetURI happy path: a valid magnet (no DHT,
	// metadata never resolves but the call accepts it).
	dl.showAddMagnetDialog()
	if e, b := findEntryAndAdd(); e != nil && b != nil {
		e.SetText("magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")
		b.OnTapped()
	}

	// Wait for the goroutine to fire.
	time.Sleep(150 * time.Millisecond)
}
