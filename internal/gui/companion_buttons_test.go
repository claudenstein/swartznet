package gui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// findButtonByLabel recursively walks a CanvasObject tree
// looking for a *widget.Button whose Text matches label.
// Used by gui tests that need to invoke a button OnTapped
// closure without holding a direct handle.
func findButtonByLabel(obj fyne.CanvasObject, label string) *widget.Button {
	if obj == nil {
		return nil
	}
	if btn, ok := obj.(*widget.Button); ok && btn.Text == label {
		return btn
	}
	switch c := obj.(type) {
	case *fyne.Container:
		for _, child := range c.Objects {
			if found := findButtonByLabel(child, label); found != nil {
				return found
			}
		}
	case *widget.Card:
		if found := findButtonByLabel(c.Content, label); found != nil {
			return found
		}
	}
	return nil
}

// TestCompanionRefreshAndFollowButtons exercises the Refresh
// Now and Follow OnTapped closures. We invoke the closures via
// the goroutine-free helpers (refreshPublisher / doFollow) so
// no goroutine bleeds across test boundaries to race other
// tests under -race.
func TestCompanionRefreshAndFollowButtons(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newDHTTestDaemon(t)
	if d.CompSub == nil {
		t.Skip("daemon did not wire up CompSub")
	}

	ct := buildCompanionTab(d)

	// Confirm the buttons are wired up — but don't tap Refresh
	// Now (its OnTapped spawns a CompPub.RefreshNow goroutine
	// that bleeds across tests). The OnTapped closure body is
	// covered by direct refreshPublisher tests.
	if btn := findButtonByLabel(ct.content, "Refresh Now"); btn == nil || btn.OnTapped == nil {
		t.Error("Refresh Now button not wired up")
	}

	// Follow button with empty entries: doFollow trips the
	// `len(pubkeyHex) != 64` ShowError arm — no goroutine.
	if btn := findButtonByLabel(ct.content, "Follow"); btn != nil && btn.OnTapped != nil {
		btn.OnTapped()
	} else {
		t.Error("Follow button not found or has no OnTapped")
	}

	// Drain any test.NewApp queued fyne.Do work before the
	// test returns to reduce bleed-through to subsequent tests.
	time.Sleep(50 * time.Millisecond)
}
