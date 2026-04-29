package gui

import (
	"testing"
	"time"

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

// TestBuildContextMenuFilesActionWithRealTorrent covers the
// "Files..." menu-item action closure with a real torrent
// in the engine so showFilesForSelected → showFilesDialog
// reaches its happy path (build a real filesDialog), then
// taps Close to drain the spawned pollLoop.
func TestBuildContextMenuFilesActionWithRealTorrent(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)
	ih := addTestTorrent(t, d.Eng)

	dl := &downloadsTab{
		d:        d,
		content:  widget.NewLabel("downloads"),
		selected: 0,
		snaps:    []engine.TorrentSnapshot{{InfoHash: ih, Name: "test", Indexing: true}},
	}
	menu := dl.buildContextMenu()
	for _, item := range menu.Items {
		if item.Label == "Files..." && item.Action != nil {
			item.Action()
		}
	}

	// showFilesDialog spawns a 2s pollLoop goroutine. Tap the
	// dialog's Close button so SetOnClosed → cancel() fires.
	for _, ov := range w.Canvas().Overlays().List() {
		for _, child := range test.LaidOutObjects(ov) {
			if btn, ok := child.(*widget.Button); ok && btn.Text == "Close" && btn.OnTapped != nil {
				btn.OnTapped()
			}
		}
	}
	time.Sleep(300 * time.Millisecond)
}

// TestBuildContextMenuRemoveActionTapsNo covers the "Remove"
// menu-item action closure. The action calls removeSelected
// which opens a confirm dialog; we tap "No" so no engine
// goroutine spawns.
func TestBuildContextMenuRemoveActionTapsNo(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	dl := &downloadsTab{
		d:        d,
		content:  widget.NewLabel("downloads"),
		selected: 0,
		snaps: []engine.TorrentSnapshot{
			{InfoHash: "0123456789abcdef0123456789abcdef01234567", Name: "test"},
		},
	}
	menu := dl.buildContextMenu()
	for _, item := range menu.Items {
		if item.Label == "Remove" && item.Action != nil {
			item.Action()
		}
	}
	for _, ov := range w.Canvas().Overlays().List() {
		for _, child := range test.LaidOutObjects(ov) {
			if btn, ok := child.(*widget.Button); ok && btn.Text == "No" && btn.OnTapped != nil {
				btn.OnTapped()
			}
		}
	}
	time.Sleep(100 * time.Millisecond)
}

// TestBuildContextMenuQueuedActionsRun invokes the menu-item
// Action closures that don't open dialogs: index-toggle,
// "Move to top of queue", and "Move to bottom of queue". The
// queue-reorder items only appear when snap.Queued is true.
// We deliberately skip "Files..." and "Remove" because their
// callbacks open dialogs (covered by the two tests above).
func TestBuildContextMenuQueuedActionsRun(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	ih := "0123456789abcdef0123456789abcdef01234567"
	dl := &downloadsTab{
		d:        d,
		content:  widget.NewLabel("downloads"),
		selected: 0,
		snaps:    []engine.TorrentSnapshot{{InfoHash: ih, Paused: false, Indexing: true, Queued: true}},
	}
	menu := dl.buildContextMenu()
	for _, item := range menu.Items {
		switch item.Label {
		case "Stop indexing", "Start indexing",
			"Move to top of queue", "Move to bottom of queue":
			if item.Action != nil {
				item.Action()
			}
		}
	}
	// Drain spawned goroutines (toggleIndex / queue moves all
	// fire engine goroutines that no-op on the missing torrent).
	time.Sleep(200 * time.Millisecond)
}
