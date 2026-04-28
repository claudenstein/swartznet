package gui

import (
	"context"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/daemon"
)

// TestCompanionPollLoopReturnsOnContextCancel covers
// companionTab.pollLoop's `case <-ctx.Done(): return` arm at
// companion.go:148-150. With an already-canceled context the
// initial refresh() call runs (covered by other tests) and the
// select picks the canceled-ctx case on the first iteration,
// returning immediately.
func TestCompanionPollLoopReturnsOnContextCancel(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	ct := &companionTab{
		d:              &daemon.Daemon{},
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

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // canceled before pollLoop starts
	ct.pollLoop(ctx)
	// pollLoop must return promptly without firing the ticker.
}
