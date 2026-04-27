package gui

import (
	"context"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestTitleLoopTickAndNotificationLoopTick covers titleLoop's
// tick.C arm at app.go:339-360 and notificationLoop's tick.C
// arm at app.go:374-385 against a real daemon. Both loops fetch
// TorrentSnapshots under their respective tick cadences (2s for
// title, 3s for notification). We let them tick once then
// cancel. Combined into one test to keep total wall time below
// ~3.5s.
func TestTitleLoopTickAndNotificationLoopTick(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("title-test")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	a := &App{
		fyne:         app,
		win:          w,
		daemon:       d,
		version:      "test",
		lastNotified: make(map[string]bool),
	}

	ctx, cancel := context.WithCancel(context.Background())
	go a.titleLoop(ctx)
	go a.notificationLoop(ctx)

	// Wait long enough for titleLoop (2s) and notificationLoop
	// (3s) to fire at least once each.
	time.Sleep(3200 * time.Millisecond)
	cancel()
	// Brief drain so goroutines exit before the test returns.
	time.Sleep(50 * time.Millisecond)
}
