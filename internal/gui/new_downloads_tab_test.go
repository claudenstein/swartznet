package gui

import (
	"context"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestNewDownloadsTabConstructs covers newDownloadsTab at
// downloads.go:59-238. The constructor builds a table, header,
// toolbar buttons, empty-state overlay, and starts pollLoop in
// a background goroutine. Daemon usage lives only in toolbar
// button callbacks and in pollLoop — passing a canceled
// context makes pollLoop return on its first iteration before
// dereferencing dl.d, so a nil daemon is safe.
func TestNewDownloadsTabConstructs(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pollLoop returns immediately on first select

	dl := newDownloadsTab(ctx, nil)
	if dl == nil {
		t.Fatal("expected non-nil downloadsTab")
	}
	if dl.table == nil {
		t.Error("expected table to be wired up")
	}
	if dl.content == nil {
		t.Error("expected content to be wired up")
	}
	if dl.selected != -1 || dl.sortCol != -1 {
		t.Errorf("expected default selected=-1 sortCol=-1, got selected=%d sortCol=%d",
			dl.selected, dl.sortCol)
	}
}
