package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/engine"
)

// TestRemoveSelectedShowsConfirmDialog covers removeSelected past
// the `if ih == "" { return }` guard. With a selected row the
// function reaches the dialog.ShowConfirm construction at
// downloads.go:723-740. The confirm callback only touches dl.d
// when ok==true, so as long as we don't trigger confirmation the
// daemon-touching path stays unexecuted. Two snap variants cover
// both label arms: one with a Name (uses snap.Name) and one
// without (falls through to the `label = ih[:16] + "..."` arm).
func TestRemoveSelectedShowsConfirmDialog(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	ih := "0123456789abcdef0123456789abcdef01234567"

	// Named torrent → uses snap.Name as label.
	dlNamed := &downloadsTab{
		content:     widget.NewLabel("downloads"),
		selectedKey: ih,
		snaps: []engine.TorrentSnapshot{
			{InfoHash: ih, Name: "ubuntu.iso"},
		},
	}
	dlNamed.removeSelected()

	// Unnamed torrent → falls back to truncated infohash label.
	dlUnnamed := &downloadsTab{
		content:     widget.NewLabel("downloads"),
		selectedKey: ih,
		snaps: []engine.TorrentSnapshot{
			{InfoHash: ih, Name: ""},
		},
	}
	dlUnnamed.removeSelected()
}
