package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/engine"
)

// TestBuildContextMenu covers buildContextMenu's arms:
// the `if ih == "" { return nil }` early-return, the
// pause/resume label switch via snap.Paused, the indexing
// label switch via snap.Indexing, the queue-reorder block
// gated on snap.Queued, and the three signature-item arms
// (no-sig / signed-trusted / signed-untrusted). All
// dl.d.Eng.* calls live inside menu-item callbacks that
// aren't invoked here, so a daemon-less downloadsTab is
// sufficient.
func TestBuildContextMenu(t *testing.T) {
	t.Parallel()

	// Early-return arm — selected = -1.
	dlEmpty := &downloadsTab{selected: -1}
	if m := dlEmpty.buildContextMenu(); m != nil {
		t.Errorf("expected nil menu for unselected row, got %+v", m)
	}

	ih := "0123456789abcdef0123456789abcdef01234567"

	// Variant matrix — exercise each branch of the menu
	// builder. We only assert the menu is non-nil; menu-item
	// label assertions would couple to copy strings.
	cases := []engine.TorrentSnapshot{
		{InfoHash: ih, Name: "ubuntu", Paused: false, Indexing: true, Queued: false},
		{InfoHash: ih, Name: "ubuntu", Paused: true, Indexing: false, Queued: true},
		{InfoHash: ih, Name: "", Paused: false, Indexing: true, Queued: true,
			SignedBy: "abcdef", TrustedPublisher: true},
		{InfoHash: ih, Name: "", Paused: true, Indexing: false, Queued: false,
			SignedBy: "abcdef", TrustedPublisher: false},
	}
	for i, snap := range cases {
		dl := &downloadsTab{
			selected: 0,
			snaps:    []engine.TorrentSnapshot{snap},
		}
		menu := dl.buildContextMenu()
		if menu == nil {
			t.Errorf("case %d: expected non-nil menu, got nil", i)
		}
	}
}

// TestBuildContextMenuPauseActionsRun covers the closures
// `pauseAction = func() { dl.pauseSelected() }` and the
// resume variant inside buildContextMenu (downloads.go:264-269).
// We invoke each menu item's Action directly so the closure
// runs against a live daemon — pauseSelected/resumeSelected
// each spawn a goroutine, so we use a real daemon and tolerate
// "torrent not found" silently.
func TestBuildContextMenuPauseActionsRun(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	ih := "0123456789abcdef0123456789abcdef01234567"

	// Paused=false → menu has "Pause" → tapping it calls pauseSelected.
	dl := &downloadsTab{
		d:        d,
		content:  widget.NewLabel("downloads"),
		selected: 0,
		snaps:    []engine.TorrentSnapshot{{InfoHash: ih, Paused: false, Indexing: true}},
	}
	menu := dl.buildContextMenu()
	for _, item := range menu.Items {
		if item.Label == "Pause" && item.Action != nil {
			item.Action()
		}
	}

	// Paused=true → menu has "Resume" → tapping it calls resumeSelected.
	dl2 := &downloadsTab{
		d:        d,
		content:  widget.NewLabel("downloads"),
		selected: 0,
		snaps:    []engine.TorrentSnapshot{{InfoHash: ih, Paused: true, Indexing: true}},
	}
	menu2 := dl2.buildContextMenu()
	for _, item := range menu2.Items {
		if item.Label == "Resume" && item.Action != nil {
			item.Action()
		}
	}
}
