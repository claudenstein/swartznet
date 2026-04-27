package gui

import (
	"testing"

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
