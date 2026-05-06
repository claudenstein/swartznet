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
//
// Asserts that both loops *exit* within a deterministic budget
// after ctx is cancelled. A regression that dropped the
// `case <-ctx.Done(): return` arm would leave the goroutines
// running until the next 2s/3s tick (or, worse, forever) — the
// 500ms budget below catches that. Also asserts that
// lastNotified survives the loop without panicking on a real
// daemon, replacing the previous "no-panic only" check.
func TestTitleLoopTickAndNotificationLoopTick(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("title-test")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)
	// Add a torrent so titleLoop's totalDown/totalUp aggregation
	// has rows to walk and notificationLoop's snapshot range
	// loop body fires.
	addTestTorrent(t, d.Eng)

	a := &App{
		fyne:         app,
		win:          w,
		daemon:       d,
		version:      "test",
		lastNotified: make(map[string]bool),
	}

	ctx, cancel := context.WithCancel(context.Background())
	titleDone := make(chan struct{})
	notifyDone := make(chan struct{})
	go func() { a.titleLoop(ctx); close(titleDone) }()
	go func() { a.notificationLoop(ctx); close(notifyDone) }()

	// Let both loops tick at least once (titleLoop 2s,
	// notificationLoop 3s) so the tick.C arm body executes.
	time.Sleep(3200 * time.Millisecond)
	cancel()

	// Both must honour ctx.Done() promptly — well before the
	// next tick fires. 500ms is a generous bound on Fyne's
	// scheduler.
	for _, c := range []struct {
		name string
		done <-chan struct{}
	}{
		{"titleLoop", titleDone}, {"notificationLoop", notifyDone},
	} {
		select {
		case <-c.done:
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("%s did not exit within 500ms of cancel — ctx.Done() arm broken", c.name)
		}
	}
}
