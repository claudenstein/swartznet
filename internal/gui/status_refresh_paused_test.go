package gui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestStatusRefreshPausedTorrent covers the
// `case "paused": paused++` arm of status.refresh
// (status.go:197). We add a real torrent then pause it so the
// engine's TorrentSnapshots returns Status="paused".
func TestStatusRefreshPausedTorrent(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)
	ih := addTestTorrent(t, d.Eng)
	if err := d.Eng.PauseTorrent(ih); err != nil {
		t.Fatalf("PauseTorrent: %v", err)
	}

	// Pause writes a flag synchronously but the next snapshot
	// reads stats; give it a moment to propagate.
	time.Sleep(50 * time.Millisecond)

	st := buildStatusTab(d)
	st.refresh()
}

