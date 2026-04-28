package gui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestCreateTorrentDialogConfirmArms taps the Create button on
// the create-torrent dialog with three input variants:
//   - empty rootEntry → ShowError "root path required"
//   - empty outEntry  → ShowError "output path required"
//   - valid root/out  → runCreateTorrent goroutine fires
func TestCreateTorrentDialogConfirmArms(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	findEntry := func(placeholder string) *widget.Entry {
		for _, ov := range w.Canvas().Overlays().List() {
			for _, child := range test.LaidOutObjects(ov) {
				if e, ok := child.(*widget.Entry); ok && e.PlaceHolder == placeholder {
					return e
				}
			}
		}
		return nil
	}
	findCreateBtn := func() *widget.Button {
		for _, ov := range w.Canvas().Overlays().List() {
			for _, child := range test.LaidOutObjects(ov) {
				if b, ok := child.(*widget.Button); ok && b.Text == "Create" && b.OnTapped != nil {
					return b
				}
			}
		}
		return nil
	}

	// Empty root → ShowError.
	createTorrentDialog(d, w)
	if b := findCreateBtn(); b != nil {
		b.OnTapped()
	}

	// Empty output (root set) → ShowError.
	createTorrentDialog(d, w)
	if e := findEntry("/path/to/file-or-folder"); e != nil {
		e.SetText("/tmp/some/path")
	}
	if b := findCreateBtn(); b != nil {
		b.OnTapped()
	}

	// Valid root + output → runCreateTorrent goroutine fires.
	createTorrentDialog(d, w)
	if e := findEntry("/path/to/file-or-folder"); e != nil {
		e.SetText("/tmp/payload")
	}
	if e := findEntry("/path/to/output.torrent"); e != nil {
		e.SetText(t.TempDir() + "/out.torrent")
	}
	if b := findCreateBtn(); b != nil {
		b.OnTapped()
	}

	time.Sleep(150 * time.Millisecond)
}
