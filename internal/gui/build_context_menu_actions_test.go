package gui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/engine"
)

// TestBuildContextMenuActions invokes every menu-item Action
// produced by buildContextMenu so each closure (pause/resume,
// copyMagnet with + without name, copyHash, queue reorder,
// signature submenu trust/revoke variants) executes its body.
func TestBuildContextMenuActions(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)
	ih := addTestTorrent(t, d.Eng)

	signed := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

	// First snap: paused + queued + signed-trusted → exercises
	// resume action, queue reorder, revoke-trust submenu item.
	dl := &downloadsTab{
		d:        d,
		content:  widget.NewLabel("downloads"),
		selected: 0,
		snaps: []engine.TorrentSnapshot{
			{
				InfoHash:         ih,
				Name:             "test",
				Paused:           true,
				Indexing:         true,
				Queued:           true,
				SignedBy:         signed,
				TrustedPublisher: true,
			},
		},
	}
	menu := dl.buildContextMenu()
	if menu == nil {
		t.Fatal("expected non-nil menu")
	}
	for _, item := range menu.Items {
		if item.Action != nil {
			item.Action()
		}
	}

	// Second snap: not paused + signed-untrusted → exercises
	// pause action, trust-publisher submenu item, copyMagnet
	// with empty Name.
	dl2 := &downloadsTab{
		d:        d,
		content:  widget.NewLabel("downloads"),
		selected: 0,
		snaps: []engine.TorrentSnapshot{
			{
				InfoHash:         ih,
				Name:             "", // exercise the no-name copyMagnet arm
				Paused:           false,
				Indexing:         false,
				SignedBy:         signed,
				TrustedPublisher: false,
			},
		},
	}
	menu2 := dl2.buildContextMenu()
	if menu2 == nil {
		t.Fatal("expected non-nil menu")
	}
	for _, item := range menu2.Items {
		if item.Action != nil {
			item.Action()
		}
	}
	// Wait briefly so any go-routines invoked from menu actions
	// have a chance to fire before the test cleans up.
	time.Sleep(100 * time.Millisecond)
}
