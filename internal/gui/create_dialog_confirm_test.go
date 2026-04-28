package gui

import (
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestCreateTorrentDialogConfirmArms taps the Create button on
// the create-torrent dialog with three input variants, exercising
// the OnConfirm closure's three arms (empty rootEntry → ShowError,
// empty outEntry → ShowError, valid → runCreateTorrent goroutine).
// A 500 ms drain at end of test bounds runCreateTorrent's
// goroutine.
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

	// Empty root → ShowError "root path required" (no goroutine).
	createTorrentDialog(d, w)
	if b := findCreateBtn(); b != nil {
		b.OnTapped()
	}

	// Empty output (root set) → ShowError "output path required"
	// (no goroutine).
	createTorrentDialog(d, w)
	if e := findEntry("/path/to/file-or-folder"); e != nil {
		e.SetText("/tmp/some/path")
	}
	if b := findCreateBtn(); b != nil {
		b.OnTapped()
	}

	// Valid root + output → runCreateTorrent goroutine fires
	// (CreateTorrentFile fails on missing path, ShowError fires).
	createTorrentDialog(d, w)
	if e := findEntry("/path/to/file-or-folder"); e != nil {
		e.SetText("/tmp/payload-does-not-exist")
	}
	if e := findEntry("/path/to/output.torrent"); e != nil {
		e.SetText(t.TempDir() + "/out.torrent")
	}
	if b := findCreateBtn(); b != nil {
		b.OnTapped()
	}

	time.Sleep(500 * time.Millisecond)
	_ = filepath.Separator // silence unused import on platforms without filepath usage
}
