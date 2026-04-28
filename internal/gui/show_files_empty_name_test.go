package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/engine"
)

// TestShowFilesForSelectedEmptyName covers the
// `if name == "" { name = s.InfoHash[:16] + "..." }` arm of
// showFilesForSelected (downloads.go:657-659). A snapshot with
// empty Name forces the fallback label. The torrent isn't in
// the daemon so showFilesDialog hits its "torrent not found"
// dialog.ShowError early-return — that's fine; the goal here is
// the empty-name arm in showFilesForSelected itself.
func TestShowFilesForSelectedEmptyName(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)
	dl := &downloadsTab{
		d:        d,
		content:  widget.NewLabel("downloads"),
		selected: 0,
		snaps: []engine.TorrentSnapshot{
			{InfoHash: "0123456789abcdef0123456789abcdef01234567", Name: ""},
		},
	}
	dl.showFilesForSelected()
}
