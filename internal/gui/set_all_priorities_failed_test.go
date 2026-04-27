package gui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/engine"
)

// TestSetAllPrioritiesFailedArm covers setAllPriorities at
// files_dialog.go:227-237. With files populated and an unknown
// infohash, the engine's SetFilePriority returns "torrent not
// found" for every entry, populating the failed slice. The
// fyne.Do callback then runs dialog.ShowError. We sleep briefly
// so the goroutine has a chance to fire.
func TestSetAllPrioritiesFailedArm(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	fd := &filesDialog{
		d:           d,
		win:         w,
		infoHashHex: "0123456789abcdef0123456789abcdef01234567",
		files: []engine.FileSnapshot{
			{Index: 0, DisplayPath: "a.txt"},
			{Index: 1, DisplayPath: "b.txt"},
		},
	}
	fd.setAllPriorities(engine.FilePriorityNormal)
	// Allow the goroutine to run-and-error and fyne.Do to fire.
	time.Sleep(150 * time.Millisecond)
}
