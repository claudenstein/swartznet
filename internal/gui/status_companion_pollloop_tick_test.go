package gui

import (
	"context"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestStatusAndCompanionPollLoopTicks covers the tick.C arms
// of statusTab.pollLoop and companionTab.pollLoop. Both run a
// 4s ticker; one combined ~4.2s wait covers both rather than
// two separate 4s tests.
func TestStatusAndCompanionPollLoopTicks(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	stCtx, stCancel := context.WithCancel(context.Background())
	cpCtx, cpCancel := context.WithCancel(context.Background())

	st := newStatusTab(stCtx, d)
	cp := newCompanionTab(cpCtx, d)
	_ = st
	_ = cp

	// Wait for both 4s tickers to fire at least once.
	time.Sleep(4200 * time.Millisecond)
	stCancel()
	cpCancel()
	time.Sleep(50 * time.Millisecond)
}
