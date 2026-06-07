package gui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestCreateTorrentDialogConfirmArms taps the Create button on
// the create-torrent dialog with three input variants, exercising
// the OnConfirm closure's arms:
//   - empty rootEntry → ShowError synchronously (no goroutine)
//   - empty outEntry (root set) → OnConfirm defensively auto-fills
//     "<root>.torrent" and reaches runCreateTorrent (goroutine)
//   - valid root + output → runCreateTorrent (goroutine)
//
// Each runCreateTorrent goroutine routes its UI mutation through
// fyne.Do, which the Fyne test driver runs inline on the spawned
// goroutine. We join each spawned goroutine via the
// afterCreateTorrent seam before opening the next dialog, keeping
// rendering single-threaded against Fyne's unsynchronized global
// font cache.
func TestCreateTorrentDialogConfirmArms(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	// afterCreateTorrent fires (under the test driver, synchronously on
	// the spawned goroutine after its fyne.Do render returns) whenever
	// an arm reaches runCreateTorrent. We push to a buffered channel so
	// the main goroutine can deterministically join that render before
	// opening the next dialog — keeping rendering single-threaded
	// against Fyne's unsynchronized global font cache.
	done := make(chan struct{}, 4)
	afterCreateTorrent = func() {
		select {
		case done <- struct{}{}:
		default:
		}
	}
	t.Cleanup(func() { afterCreateTorrent = nil })

	// joinSpawned blocks for one afterCreateTorrent signal. Call it only
	// for arms that actually reach runCreateTorrent, so the spawned
	// goroutine's render is fully drained before any further rendering.
	joinSpawned := func() {
		t.Helper()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("runCreateTorrent goroutine did not complete")
		}
	}

	// closeOverlays dismisses any open dialogs so each arm operates on a
	// single, freshly opened dialog. Without this the three stacked
	// create dialogs share the canvas overlay list and findEntry/
	// findCreateBtn would match widgets from an earlier arm,
	// nondeterministically routing taps to the wrong (already-spawning)
	// closure.
	closeOverlays := func() {
		for {
			top := w.Canvas().Overlays().Top()
			if top == nil {
				return
			}
			w.Canvas().Overlays().Remove(top)
		}
	}

	findEntry := func(placeholder string) *widget.Entry {
		for _, ov := range w.Canvas().Overlays().List() {
			for _, child := range test.LaidOutObjects(ov) {
				if e, ok := child.(*widget.Entry); ok && e.PlaceHolder == placeholder {
					return e
				}
			}
		}
		return nil
	}
	findCreateBtn := func() *widget.Button {
		for _, ov := range w.Canvas().Overlays().List() {
			for _, child := range test.LaidOutObjects(ov) {
				if b, ok := child.(*widget.Button); ok && b.Text == "Create" && b.OnTapped != nil {
					return b
				}
			}
		}
		return nil
	}

	// Empty root → ShowError "root path required" (no goroutine).
	createTorrentDialog(d, w)
	if b := findCreateBtn(); b != nil {
		b.OnTapped()
	}
	closeOverlays()

	// Empty output (root set): OnConfirm's defensive autofill kicks
	// in ("<root>.torrent") and reaches runCreateTorrent, so this arm
	// also spawns the async UI goroutine. We clear the auto-filled
	// output back to empty to exercise that defensive path, then join
	// the spawned goroutine before moving on.
	createTorrentDialog(d, w)
	if e := findEntry("/path/to/file-or-folder"); e != nil {
		e.SetText("/tmp/some/path")
	}
	if e := findEntry("/path/to/output.torrent"); e != nil {
		e.SetText("")
	}
	if b := findCreateBtn(); b != nil {
		b.OnTapped()
	}
	joinSpawned()
	closeOverlays()

	// Valid root + output → runCreateTorrent goroutine fires
	// (CreateTorrentFile fails on missing path, ShowError fires).
	createTorrentDialog(d, w)
	if e := findEntry("/path/to/file-or-folder"); e != nil {
		e.SetText("/tmp/payload-does-not-exist")
	}
	if e := findEntry("/path/to/output.torrent"); e != nil {
		e.SetText(t.TempDir() + "/out.torrent")
	}
	if b := findCreateBtn(); b != nil {
		b.OnTapped()
	}

	// Join the runCreateTorrent goroutine spawned by the valid arm
	// before the test returns (and before the deferred teardown
	// renders), so its inline fyne.Do render does not race other
	// rendering against Fyne's unsynchronized global font cache.
	joinSpawned()
}
