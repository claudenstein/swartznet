package gui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestFilesDialogBulkButtonsRealTorrent covers the
// Select All / Deselect All button OnTapped closures in
// filesDialog.build. With a real torrent in the engine,
// setAllPriorities's SetFilePriority calls succeed (no err
// dialog rendering, which would race other tests). The
// resulting goroutine completes silently.
func TestFilesDialogBulkButtonsRealTorrent(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)
	ih := addTestTorrent(t, d.Eng)

	files, err := d.Eng.TorrentFiles(ih)
	if err != nil {
		t.Fatalf("TorrentFiles: %v", err)
	}

	fd := &filesDialog{
		d:           d,
		win:         w,
		infoHashHex: ih,
		files:       files,
	}
	fd.build("test torrent")

	var allBtn, noneBtn *widget.Button
	for _, ov := range w.Canvas().Overlays().List() {
		for _, child := range test.LaidOutObjects(ov) {
			if btn, ok := child.(*widget.Button); ok {
				switch btn.Text {
				case "Select All":
					allBtn = btn
				case "Deselect All":
					noneBtn = btn
				}
			}
		}
	}
	if allBtn != nil && allBtn.OnTapped != nil {
		allBtn.OnTapped()
	}
	if noneBtn != nil && noneBtn.OnTapped != nil {
		noneBtn.OnTapped()
	}

	fd.dlg.Hide()
	time.Sleep(200 * time.Millisecond)
}
