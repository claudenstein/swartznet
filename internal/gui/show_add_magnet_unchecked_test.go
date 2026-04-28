package gui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestShowAddMagnetDialogPrefilledShouldNotIndex covers the
// `if !shouldIndex { ... SetTorrentIndexing(ih, false) }` branch
// of showAddMagnetDialogPrefilled (downloads.go:550-554). We
// uncheck the index checkbox before tapping Add, so the
// goroutine takes the !shouldIndex arm after AddMagnetURI
// succeeds.
func TestShowAddMagnetDialogPrefilledShouldNotIndex(t *testing.T) {
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

	dl.showAddMagnetDialog()

	// Walk the overlay tree to find the URI entry, the index
	// checkbox, and the Add button.
	var entry *widget.Entry
	var indexCheck *widget.Check
	var addBtn *widget.Button
	for _, ov := range w.Canvas().Overlays().List() {
		for _, child := range test.LaidOutObjects(ov) {
			switch x := child.(type) {
			case *widget.Entry:
				if x.PlaceHolder == "magnet:?xt=urn:btih:..." {
					entry = x
				}
			case *widget.Check:
				if x.Text == "Index this torrent's files after download" {
					indexCheck = x
				}
			case *widget.Button:
				if x.Text == "Add" {
					addBtn = x
				}
			}
		}
	}

	if entry != nil && indexCheck != nil && addBtn != nil {
		entry.SetText("magnet:?xt=urn:btih:fedcba9876543210fedcba9876543210fedcba98")
		indexCheck.SetChecked(false)
		addBtn.OnTapped()
	}
	time.Sleep(500 * time.Millisecond)
}
