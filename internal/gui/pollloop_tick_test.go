package gui

import (
	"context"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestStatusPollLoopTickFires covers status.pollLoop's
// `case <-tick.C: st.refresh()` arm by running the goroutine
// for slightly more than the 4-second ticker interval. The
// initial refresh + one tick-driven refresh both fire fyne.Do
// label updates; we cancel after 4.3s and drain.
func TestStatusPollLoopTickFires(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)
	st := buildStatusTab(d)

	ctx, cancel := context.WithCancel(context.Background())
	go st.pollLoop(ctx)
	time.Sleep(4300 * time.Millisecond)
	cancel()
	time.Sleep(200 * time.Millisecond)
	_ = st
}

// TestCompanionPollLoopTickFires covers companion.pollLoop's
// `case <-tick.C: ct.refresh()` arm. Same pattern as the status
// version.
func TestCompanionPollLoopTickFires(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)
	ct := buildCompanionTab(d)

	ctx, cancel := context.WithCancel(context.Background())
	go ct.pollLoop(ctx)
	time.Sleep(4300 * time.Millisecond)
	cancel()
	time.Sleep(200 * time.Millisecond)
	_ = ct
}
