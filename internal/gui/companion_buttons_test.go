package gui

import (
	"context"
	"testing"

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

// TestCompanionRefreshAndFollowButtons taps the "Refresh Now"
// and "Follow" buttons inside the companion tab so each
// OnTapped closure executes (covers refreshPublisher invocation
// and doFollow invocation through the form button).
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ct := newCompanionTab(ctx, d)

	if btn := findButtonByLabel(ct.content, "Refresh Now"); btn != nil && btn.OnTapped != nil {
		btn.OnTapped()
	} else {
		t.Error("Refresh Now button not found or has no OnTapped")
	}

	// Follow button needs the form's pubkey + label entries set
	// before it has anything sensible to do — we just confirm
	// the OnTapped doesn't panic when entries are empty.
	if btn := findButtonByLabel(ct.content, "Follow"); btn != nil && btn.OnTapped != nil {
		btn.OnTapped()
	} else {
		t.Error("Follow button not found or has no OnTapped")
	}
}
