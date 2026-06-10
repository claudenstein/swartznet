package gui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/engine"
)

// TestBuildContextMenuActions invokes the Action closures of
// menu items that DON'T spawn engine goroutines: copyMagnet
// (with + without name), copyHash, signature-submenu items
// (Verify, Trust, Revoke, Copy publisher pubkey). The
// goroutine-spawning items (Files..., Pause, Resume, Remove,
// Toggle Index, Queue reorder) are skipped here because their
// goroutines bleed across test boundaries and race other
// tests under -race.
func TestBuildContextMenuActions(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	ih := "0123456789abcdef0123456789abcdef01234567"
	signed := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

	// Names of menu items whose Action spawns a goroutine; we
	// invoke everything except these.
	skip := map[string]bool{
		"Files...":                true,
		"Pause":                   true,
		"Resume":                  true,
		"Remove":                  true,
		"Stop indexing":           true,
		"Start indexing":          true,
		"Move to top of queue":    true,
		"Move to bottom of queue": true,
	}
	tap := func(menu *fyne.Menu) {
		for _, item := range menu.Items {
			if item.Action == nil || skip[item.Label] {
				continue
			}
			item.Action()
		}
	}

	// First snap: paused + queued + signed-trusted → revoke-trust
	// submenu item.
	dl := &downloadsTab{
		d:           d,
		content:     widget.NewLabel("downloads"),
		selectedKey: ih,
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
	tap(menu)

	// Second snap: not paused + signed-untrusted → trust-publisher
	// submenu item, copyMagnet with empty Name.
	dl2 := &downloadsTab{
		d:           d,
		content:     widget.NewLabel("downloads"),
		selectedKey: ih,
		snaps: []engine.TorrentSnapshot{
			{
				InfoHash:         ih,
				Name:             "",
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
	tap(menu2)
}
