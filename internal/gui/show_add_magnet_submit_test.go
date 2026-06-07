package gui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// joinAddMagnet arms the afterAddMagnet test seam with a channel
// buffered for up to n goroutines, and returns a wait func that blocks
// until one showAddMagnetDialogPrefilled goroutine (including any inline
// fyne.Do error render under the test driver) completes. Call wait once
// per expected goroutine. Replaces time.Sleep so the async render is
// drained deterministically against Fyne's unsynchronized global caches.
func joinAddMagnet(t *testing.T, n int) func() {
	t.Helper()
	done := make(chan struct{}, n)
	afterAddMagnet = func() {
		select {
		case done <- struct{}{}:
		default:
		}
	}
	t.Cleanup(func() { afterAddMagnet = nil })
	return func() {
		t.Helper()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("showAddMagnetDialogPrefilled goroutine did not complete")
		}
	}
}

// TestShowAddMagnetDialogPrefilledSubmit covers the submit
// callback closure in showAddMagnetDialogPrefilled. With the
// dialog rendered via overlay we locate the URI Entry, set
// various texts, and tap the "Add" button to drive the
// validation arms: empty-URI and validateMagnetURI rejection.
// The engine.AddMagnetURI happy-path arm is intentionally
// skipped — it spawns a goroutine that calls real engine work
// and bleeds across test boundaries to race other tests'
// Fyne caches under -race.
func TestShowAddMagnetDialogPrefilledSubmit(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)
	dl := &downloadsTab{
		d:       d,
		content: widget.NewLabel("downloads"),
	}

	wait := joinAddMagnet(t, 1)

	// closeOverlays dismisses any open dialogs so each arm operates on
	// a single, freshly opened dialog — otherwise stacked dialogs share
	// the overlay list and findEntryAndAdd could match widgets from an
	// earlier arm.
	closeOverlays := func() {
		for {
			top := w.Canvas().Overlays().Top()
			if top == nil {
				return
			}
			w.Canvas().Overlays().Remove(top)
		}
	}

	// Helper to find the dialog's URI entry + Add button on the
	// canvas overlay stack.
	findEntryAndAdd := func() (*widget.Entry, *widget.Button) {
		var entry *widget.Entry
		var addBtn *widget.Button
		for _, ov := range w.Canvas().Overlays().List() {
			for _, child := range test.LaidOutObjects(ov) {
				switch x := child.(type) {
				case *widget.Entry:
					if x.PlaceHolder == "magnet:?xt=urn:btih:..." {
						entry = x
					}
				case *widget.Button:
					if x.Text == "Add" {
						addBtn = x
					}
				}
			}
		}
		return entry, addBtn
	}

	// Empty-URI arm: Add with empty entry → showAddMagnetError
	// (synchronous, no goroutine spawned).
	dl.showAddMagnetDialog()
	if e, b := findEntryAndAdd(); e != nil && b != nil {
		e.SetText("")
		b.OnTapped()
	}
	closeOverlays()

	// validateMagnetURI rejection arm: bad scheme (synchronous, no
	// goroutine spawned).
	dl.showAddMagnetDialog()
	if e, b := findEntryAndAdd(); e != nil && b != nil {
		e.SetText("http://not-a-magnet")
		b.OnTapped()
	}
	closeOverlays()

	// Engine.AddMagnetURI happy path: valid magnet URI with
	// shouldIndex=true (default). Spawns goroutine that calls
	// AddMagnetURI; with DHT off, the call adds the torrent and
	// returns quickly. Join it via the afterAddMagnet seam.
	dl.showAddMagnetDialog()
	if e, b := findEntryAndAdd(); e != nil && b != nil {
		e.SetText("magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")
		b.OnTapped()
	}
	wait()
}
