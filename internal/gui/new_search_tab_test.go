package gui

import (
	"context"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestNewSearchTabConstructs covers newSearchTab at
// search.go:45-103. The constructor builds queryEntry,
// layer checkboxes, limit entry, search button, status
// label, progress bar, result box, empty-state panel, and
// the content border. The daemon is captured in callbacks
// (onSubmitted, search button) but never invoked here.
func TestNewSearchTabConstructs(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	st := newSearchTab(context.Background(), nil)
	if st == nil {
		t.Fatal("expected non-nil searchTab")
	}
	if st.queryEntry == nil || st.searchBtn == nil || st.content == nil {
		t.Error("expected core widgets wired up")
	}
	if !st.localChk.Checked {
		t.Error("local checkbox should default to checked")
	}
	if st.limitEntry.Text != "20" {
		t.Errorf("limit entry default = %q, want %q", st.limitEntry.Text, "20")
	}
}
