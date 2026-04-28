package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestShowFilesDialogNoMetadata covers showFilesDialog at
// files_dialog.go:27-31 — the `len(files) == 0` arm. We add a
// magnet URI so the engine knows about the infohash but with
// DHT disabled the metadata never arrives, so TorrentFiles
// returns an empty slice and the function shows the
// "Waiting for metadata" info dialog.
func TestShowFilesDialogNoMetadata(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	// A valid magnet URI with no metadata source — the engine
	// adds the torrent but stays in pending-metadata state.
	const magnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
	ih, err := d.Eng.AddMagnetURI(magnet)
	if err != nil {
		t.Fatalf("AddMagnetURI: %v", err)
	}
	showFilesDialog(d, w, ih, "no-metadata")
}
