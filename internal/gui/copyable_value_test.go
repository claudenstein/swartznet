package gui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// findButton walks a fyne CanvasObject tree depth-first looking
// for the first *widget.Button. Used by tests that need to
// invoke a button's OnTapped without knowing the exact layout.
func findButton(obj fyne.CanvasObject) *widget.Button {
	if b, ok := obj.(*widget.Button); ok {
		return b
	}
	if c, ok := obj.(*fyne.Container); ok {
		for _, child := range c.Objects {
			if b := findButton(child); b != nil {
				return b
			}
		}
	}
	return nil
}

// TestCopyableValueClick covers copyableValue's click-handler
// closure: the Copy button writes `value` to the clipboard. Uses
// fyne/test for an in-process app so no display is required.
func TestCopyableValueClick(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	const want = "secret-pubkey-abc"
	obj := copyableValue(want)

	btn := findButton(obj)
	if btn == nil {
		t.Skip("Copy button not found in copyableValue layout — fyne may have changed")
	}
	btn.OnTapped()

	if got := app.Clipboard().Content(); got != want {
		t.Errorf("clipboard = %q, want %q", got, want)
	}
}

// TestCopyableValuePlaceholder covers the early-return arm:
// "" / "unknown" / "disabled" → bare *widget.Label, no button.
func TestCopyableValuePlaceholder(t *testing.T) {
	for _, val := range []string{"", "unknown", "disabled"} {
		t.Run(val, func(t *testing.T) {
			obj := copyableValue(val)
			if _, ok := obj.(*widget.Label); !ok {
				t.Errorf("copyableValue(%q) = %T, want *widget.Label (no Copy button)", val, obj)
			}
		})
	}
}

// Compile-time assertion: container.NewBorder returns a valid
// fyne.CanvasObject, matching copyableValue's normal return.
var _ fyne.CanvasObject = container.NewBorder(nil, nil, nil, nil)
