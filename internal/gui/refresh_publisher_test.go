package gui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// joinRefreshPublisher arms the afterRefreshPublisher test seam and
// returns a wait func that blocks until refreshPublisher's spawned
// goroutine (including any inline fyne.Do error render under the test
// driver) has fully completed. This replaces time.Sleep so the async
// render is drained deterministically, keeping rendering
// single-threaded against Fyne's unsynchronized global caches.
func joinRefreshPublisher(t *testing.T) func() {
	t.Helper()
	done := make(chan struct{}, 1)
	afterRefreshPublisher = func() {
		select {
		case done <- struct{}{}:
		default:
		}
	}
	t.Cleanup(func() { afterRefreshPublisher = nil })
	return func() {
		t.Helper()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("refreshPublisher goroutine did not complete")
		}
	}
}

// TestRefreshPublisherWithCompPub covers refreshPublisher's
// non-nil-CompPub arm. CompPub.RefreshNow on a fresh daemon with
// no indexed content returns nil (nothing to publish), so the
// goroutine completes without firing fyne.Do(ShowError). We join
// the goroutine via the afterRefreshPublisher seam before the test
// returns.
func TestRefreshPublisherWithCompPub(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newDHTTestDaemon(t)
	if d.CompPub == nil {
		skipMissing(t, d, "CompPub")
		return
	}

	wait := joinRefreshPublisher(t)

	ct := &companionTab{
		d:       d,
		content: widget.NewLabel("companion"),
	}
	ct.refreshPublisher()
	wait()
}

// TestRefreshPublisherThrottledShowsError covers the err arm of
// refreshPublisher's spawned goroutine. The publisher's initial
// refreshOnce (run on Start) fails on the empty index and calls
// recordFailure which sets lastRefresh = time.Now(). A
// subsequent RefreshNow within MinInterval (default 1 min) then
// returns ErrTooSoon, which the GUI's goroutine surfaces via
// fyne.Do(dialog.ShowError). We wait 200 ms after daemon
// construction to give the publisher's run goroutine time to
// finish that initial refreshOnce and stamp lastRefresh, then join
// the GUI goroutine (which renders the error) via the seam.
func TestRefreshPublisherThrottledShowsError(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newDHTTestDaemon(t)
	if d.CompPub == nil {
		skipMissing(t, d, "CompPub")
		return
	}

	// Wait for the publisher's startup refreshOnce to set
	// lastRefresh (recordFailure on empty index). This is a genuine
	// poll for a different goroutine's state, not a fyne.Do join.
	time.Sleep(200 * time.Millisecond)

	wait := joinRefreshPublisher(t)

	ct := &companionTab{
		d:       d,
		content: widget.NewLabel("companion"),
	}
	ct.refreshPublisher()
	wait()
}
