package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/daemon"
	"github.com/swartznet/swartznet/internal/engine"
)

// TestBindPrioSelectDetachesStaleHandler proves the recycled-widget
// fix in files_dialog.go: when a priority Select is re-bound to a
// new row, bindPrioSelect must clear the PREVIOUS row's OnChanged
// before SetSelected fires, so SetSelected does not invoke the stale
// closure (which captured the previous row's file index). A
// regression that set the value before nil-ing OnChanged would fire
// the stale handler and write the wrong file's priority.
func TestBindPrioSelectDetachesStaleHandler(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	fd := &filesDialog{
		d:           &daemon.Daemon{},
		win:         w,
		infoHashHex: "0123456789abcdef0123456789abcdef01234567",
	}

	sel := widget.NewSelect([]string{"none", "normal", "high"}, nil)

	// Simulate a widget that was previously bound to row A (index 7)
	// and is now recycled. Attach a sentinel as the "stale" handler.
	staleFired := 0
	sel.SetSelected("none")
	sel.OnChanged = func(string) { staleFired++ }

	// Re-bind to row B with a DIFFERENT priority value. SetSelected
	// will change the value from "none" to "high"; if the stale
	// handler were still attached it would fire here.
	fd.bindPrioSelect(sel, engine.FileSnapshot{Index: 99, Priority: "high"})

	if staleFired != 0 {
		t.Fatalf("stale OnChanged fired %d time(s) during re-bind — wrong-file write bug", staleFired)
	}
	if sel.Selected != "high" {
		t.Fatalf("after bind sel.Selected = %q, want \"high\"", sel.Selected)
	}
}

// TestBindPrioSelectSkipsNoOpWrite asserts the freshly-bound handler
// short-circuits when the chosen value equals the rendered priority,
// so no redundant engine write is issued. We deliberately do NOT
// invoke the changed-value branch here: that spawns a background
// SetFilePriority goroutine whose error path calls fyne.Do +
// dialog.ShowError, which would bleed Fyne global-state mutations
// across the test boundary and race the next test under -race
// (the same bleed the existing files_dialog tests avoid by never
// triggering a real write). The new behaviour worth pinning is the
// same-value early return, which touches no shared state.
func TestBindPrioSelectSkipsNoOpWrite(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	fd := &filesDialog{
		d:           &daemon.Daemon{},
		win:         w,
		infoHashHex: "0123456789abcdef0123456789abcdef01234567",
	}

	sel := widget.NewSelect([]string{"none", "normal", "high"}, nil)
	fd.bindPrioSelect(sel, engine.FileSnapshot{Index: 0, Priority: "normal"})

	if sel.OnChanged == nil {
		t.Fatal("OnChanged not bound")
	}
	// Same-value invoke must early-return: no panic, no engine
	// goroutine spawned (fd.d.Eng is nil, so reaching the engine
	// branch would panic — this asserts the guard fires first).
	sel.OnChanged("normal")
}
