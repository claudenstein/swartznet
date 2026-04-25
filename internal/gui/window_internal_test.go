package gui

import (
	"testing"

	"fyne.io/fyne/v2"
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
