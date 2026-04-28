package gui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/engine"
)

// TestSetAllPrioritiesWithFiles covers setAllPriorities's
// failed-arm at files_dialog.go:227-237. With non-empty files
// and an unknown infohash, the goroutine's SetFilePriority calls
// all err and the failed slice gets populated; fyne.Do then
// runs dialog.ShowError. A 500 ms drain at end of test lets the
// goroutine fully complete.
func TestSetAllPrioritiesWithFiles(t *testing.T) {
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
	time.Sleep(500 * time.Millisecond)
}
