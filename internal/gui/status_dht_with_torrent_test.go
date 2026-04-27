package gui

import (
	"context"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestStatusRefreshDHTWithTorrent covers status.refresh's
// snaps loop body at status.go:194-208 and the Publisher
// non-nil arm at status.go:275-279. With DHT enabled via
// newDHTTestDaemon, the engine's publisher is wired up;
// adding a torrent populates TorrentSnapshots so each
// case in the status switch fires.
func TestStatusRefreshDHTWithTorrent(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newDHTTestDaemon(t)
	if d.Eng.Lookup() == nil {
		t.Skip("daemon did not wire up Lookup")
	}
	addTestTorrent(t, d.Eng)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	st := newStatusTab(ctx, d)
	st.refresh()
}
