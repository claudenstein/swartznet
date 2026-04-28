package gui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/daemon"
)

// TestCompanionRefreshNilSubsystems covers companionTab.refresh
// at companion.go:157-218 when neither CompPub nor CompSub is
// wired. Both nil-checks short-circuit so no publisher / follow
// data is gathered, but the fyne.Do block still executes and
// resets every label to its empty state. We need every widget
// field populated so the fyne.Do callback doesn't dereference a
// nil pointer.
func TestCompanionRefreshNilSubsystems(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	ct := &companionTab{
		d:              &daemon.Daemon{}, // CompPub and CompSub both nil
		pubKeyLbl:      widget.NewLabel(""),
		pubRefreshLbl:  widget.NewLabel(""),
		pubCountLbl:    widget.NewLabel(""),
		pubErrorLbl:    widget.NewLabel(""),
		pubInfoHashLbl: widget.NewLabel(""),
		followsEmpty:   widget.NewLabel(""),
		followList: widget.NewList(
			func() int { return 0 },
			func() fyne.CanvasObject { return widget.NewLabel("") },
			func(widget.ListItemID, fyne.CanvasObject) {},
		),
	}
	ct.refresh()
}
