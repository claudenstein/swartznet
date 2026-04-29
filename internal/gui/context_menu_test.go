package gui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestNewRightClickCaptureWiring covers newRightClickCapture's
// constructor and CreateRenderer — the renderer wraps the child
// object so the widget displays its content unchanged.
func TestNewRightClickCaptureWiring(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	child := widget.NewLabel("inner")
	r := newRightClickCapture(child, func() *fyne.Menu { return nil })
	if r.child != child {
		t.Errorf("child not wired through")
	}
	if got := r.CreateRenderer(); got == nil {
		t.Error("CreateRenderer returned nil")
	}
}

// TestRightClickCaptureNilBuilderShortCircuits covers
// TappedSecondary's `if r.menuBuilder == nil { return }` arm.
// A wrapper with nil menuBuilder must not panic on right-click.
func TestRightClickCaptureNilBuilderShortCircuits(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	child := widget.NewLabel("inner")
	r := newRightClickCapture(child, nil)
	r.TappedSecondary(&fyne.PointEvent{})
}

// TestRightClickCaptureEmptyMenuShortCircuits covers
// TappedSecondary's `if menu == nil || len(menu.Items) == 0`
// arm. Both nil-menu and empty-items are no-ops.
func TestRightClickCaptureEmptyMenuShortCircuits(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	child := widget.NewLabel("inner")
	r := newRightClickCapture(child, func() *fyne.Menu { return nil })
	r.TappedSecondary(&fyne.PointEvent{}) // nil menu

	r2 := newRightClickCapture(child, func() *fyne.Menu {
		return &fyne.Menu{Items: nil}
	})
	r2.TappedSecondary(&fyne.PointEvent{}) // empty items
}

// TestRightClickCaptureNilCanvas covers TappedSecondary's
// `canvas := fyne.CurrentApp().Driver().CanvasForObject(r); if
// canvas == nil { return }` arm. An object never attached to any
// canvas resolves CanvasForObject to nil.
func TestRightClickCaptureNilCanvas(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	child := widget.NewLabel("inner")
	r := newRightClickCapture(child, func() *fyne.Menu {
		return fyne.NewMenu("ctx", fyne.NewMenuItem("act", func() {}))
	})
	// r is never SetContent'd anywhere, so its canvas is nil.
	r.TappedSecondary(&fyne.PointEvent{})
}

// TestRightClickCaptureHappyPath covers TappedSecondary's final
// statement — ShowPopUpMenuAtPosition — by attaching the
// wrapper to a window so CanvasForObject returns non-nil and
// the menu actually pops.
func TestRightClickCaptureHappyPath(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()

	child := widget.NewLabel("inner")
	r := newRightClickCapture(child, func() *fyne.Menu {
		return fyne.NewMenu("ctx", fyne.NewMenuItem("act", func() {}))
	})
	w.SetContent(r)
	r.TappedSecondary(&fyne.PointEvent{
		AbsolutePosition: fyne.NewPos(10, 10),
	})
}
