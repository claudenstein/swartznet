package gui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestWindowForObjectNilGuards covers the early-return paths
// that don't require a Fyne app: nil obj should always return
// nil. The "nil app" case typically can't be reached in tests
// (Fyne creates a current app when one is asked for); we only
// test the nil-obj guard so we don't accidentally trigger
// app-init side effects.
func TestWindowForObjectNilGuards(t *testing.T) {
	t.Parallel()
	if got := windowForObject(nil); got != nil {
		t.Errorf("windowForObject(nil) = %v, want nil", got)
	}
}

// Compile-time assertion that the helper signature matches
// the fyne.Window interface. Catches future Fyne API drift.
var _ func(fyne.CanvasObject) fyne.Window = windowForObject

// TestWindowForObjectInWindow exercises the success path: an
// object placed inside a real test-driver window resolves to
// that window via the canvas-identity match (lines 27 + 38-41).
func TestWindowForObjectInWindow(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	w := app.NewWindow("test")
	defer w.Close()
	label := widget.NewLabel("hello")
	w.SetContent(label)

	got := windowForObject(label)
	if got == nil {
		t.Fatal("windowForObject returned nil for an object inside a real window")
	}
	if got.Canvas() != w.Canvas() {
		t.Errorf("windowForObject returned a different window's canvas")
	}
}

// TestWindowForObjectFallsBackOnUnattachedObject covers the
// `canvas == nil → for _, w := range AllWindows() { return w }`
// fallback arm (lines 33-35). An object not attached to any
// canvas resolves to the first known window so dialogs remain
// reachable during startup.
func TestWindowForObjectFallsBackOnUnattachedObject(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	w := app.NewWindow("fallback")
	defer w.Close()
	w.SetContent(widget.NewLabel("attached"))

	// orphan is freshly constructed and never SetContent'd anywhere.
	orphan := widget.NewLabel("orphan")
	got := windowForObject(orphan)
	if got == nil {
		t.Fatal("expected fallback window, got nil")
	}
}
