package gui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestRefreshPublisherWithCompPub covers refreshPublisher's
// non-nil-CompPub arm. CompPub.RefreshNow on a fresh daemon with
// no indexed content returns nil (nothing to publish), so the
// goroutine completes without firing fyne.Do(ShowError). A 200 ms
// drain at end of test lets the goroutine fully exit before the
// test returns.
func TestRefreshPublisherWithCompPub(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newDHTTestDaemon(t)
	if d.CompPub == nil {
		t.Skip("daemon did not wire up CompPub")
	}

	ct := &companionTab{
		d:       d,
		content: widget.NewLabel("companion"),
	}
	ct.refreshPublisher()
	time.Sleep(200 * time.Millisecond)
}

// TestRefreshPublisherThrottledShowsError covers the err arm of
// refreshPublisher's spawned goroutine. The publisher's initial
// refreshOnce (run on Start) fails on the empty index and calls
// recordFailure which sets lastRefresh = time.Now(). A
// subsequent RefreshNow within MinInterval (default 1 min) then
// returns ErrTooSoon, which the GUI's goroutine surfaces via
// fyne.Do(dialog.ShowError). We wait 200 ms after daemon
// construction to give the publisher's run goroutine time to
// finish that initial refreshOnce and stamp lastRefresh.
func TestRefreshPublisherThrottledShowsError(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newDHTTestDaemon(t)
	if d.CompPub == nil {
		t.Skip("daemon did not wire up CompPub")
	}

	// Wait for the publisher's startup refreshOnce to set
	// lastRefresh (recordFailure on empty index).
	time.Sleep(200 * time.Millisecond)

	ct := &companionTab{
		d:       d,
		content: widget.NewLabel("companion"),
	}
	ct.refreshPublisher()
	time.Sleep(300 * time.Millisecond)
}
