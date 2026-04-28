package gui

import (
	"context"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestStatusPollLoopSync covers status.pollLoop's body
// (initial refresh + ctx.Done early-return) by calling it
// synchronously with a canceled context. The goroutine never
// spawns, so no fyne.Do bleed.
func TestStatusPollLoopSync(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)
	st := buildStatusTab(d)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	st.pollLoop(ctx)
}

// TestCompanionPollLoopSync covers companion.pollLoop's body
// (initial refresh + ctx.Done early-return) by calling it
// synchronously with a canceled context.
func TestCompanionPollLoopSync(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)
	ct := buildCompanionTab(d)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ct.pollLoop(ctx)
}
