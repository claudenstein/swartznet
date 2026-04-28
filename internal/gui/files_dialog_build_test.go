package gui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/engine"
)

// TestFilesDialogBuild covers filesDialog.build at
// files_dialog.go:55-156. The build function constructs the
// list, sort dropdown, bulk buttons, and dialog, then spawns
// pollLoop and shows the dialog. We hand-populate fd.files,
// call build, then immediately Hide() so the OnClosed callback
// cancels the goroutine context and pollLoop exits on the next
// select. A 100 ms drain at end of test lets that happen.
func TestFilesDialogBuild(t *testing.T) {
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
		},
	}
	fd.build("test torrent")
	fd.dlg.Hide() // triggers SetOnClosed → cancel(), pollLoop exits
	time.Sleep(100 * time.Millisecond)
}
