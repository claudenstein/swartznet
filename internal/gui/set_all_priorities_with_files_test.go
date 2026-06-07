package gui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/engine"
)

// joinSetAllPriorities arms the afterSetAllPriorities test seam with a
// channel buffered for up to n goroutines, and returns a wait func that
// blocks until one setAllPriorities goroutine (including any inline
// fyne.Do error render under the test driver) completes. Call wait once
// per expected goroutine. This replaces time.Sleep so the async render
// is drained deterministically, keeping rendering single-threaded
// against Fyne's unsynchronized global caches.
func joinSetAllPriorities(t *testing.T, n int) func() {
	t.Helper()
	done := make(chan struct{}, n)
	afterSetAllPriorities = func() {
		select {
		case done <- struct{}{}:
		default:
		}
	}
	t.Cleanup(func() { afterSetAllPriorities = nil })
	return func() {
		t.Helper()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("setAllPriorities goroutine did not complete")
		}
	}
}

// TestSetAllPrioritiesWithFiles covers setAllPriorities's
// failed-arm at files_dialog.go:227-237. With non-empty files
// and an unknown infohash, the goroutine's SetFilePriority calls
// all err and the failed slice gets populated; fyne.Do then
// runs dialog.ShowError. A 500 ms drain at end of test lets the
// goroutine fully complete.
func TestSetAllPrioritiesWithFiles(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	wait := joinSetAllPriorities(t, 1)

	fd := &filesDialog{
		d:           d,
		win:         w,
		infoHashHex: "0123456789abcdef0123456789abcdef01234567",
		files: []engine.FileSnapshot{
			{Index: 0, DisplayPath: "a.txt"},
			{Index: 1, DisplayPath: "b.txt"},
		},
	}
	fd.setAllPriorities(engine.FilePriorityNormal)
	wait()
}
