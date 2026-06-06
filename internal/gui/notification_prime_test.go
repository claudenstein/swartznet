package gui

import (
	"testing"

	"github.com/swartznet/swartznet/internal/engine"
)

// TestPollNotificationsPrimesWithoutNotifying proves the startup
// fix: on the first poll (primed=false) every already-seeding
// torrent is recorded in lastNotified but produces NO notification.
// Those torrents finished in a previous session, so a "Download
// complete" toast on startup would be a false positive.
func TestPollNotificationsPrimesWithoutNotifying(t *testing.T) {
	t.Parallel()

	a := &App{lastNotified: make(map[string]bool)}

	snaps := []engine.TorrentSnapshot{
		{InfoHash: "aa", Name: "already-1", Status: "seeding"},
		{InfoHash: "bb", Name: "already-2", Status: "seeding"},
		{InfoHash: "cc", Name: "downloading", Status: "downloading"},
	}

	got := a.pollNotifications(snaps, false /* first poll */)
	if len(got) != 0 {
		t.Fatalf("first poll emitted %d notifications, want 0 (false-positive startup toasts)", len(got))
	}
	// Both already-seeding torrents must be primed into lastNotified.
	if !a.lastNotified["aa"] || !a.lastNotified["bb"] {
		t.Fatalf("first poll did not prime lastNotified: %v", a.lastNotified)
	}
	// The downloading torrent must NOT be primed.
	if a.lastNotified["cc"] {
		t.Fatal("downloading torrent should not be primed into lastNotified")
	}
}

// TestPollNotificationsNotifiesOnTransition proves that after the
// prime poll, a torrent observed transitioning into seeding fires
// exactly one notification, and only once.
func TestPollNotificationsNotifiesOnTransition(t *testing.T) {
	t.Parallel()

	a := &App{lastNotified: make(map[string]bool)}

	// Prime poll: one torrent already seeding.
	prime := []engine.TorrentSnapshot{
		{InfoHash: "aa", Name: "already", Status: "seeding"},
	}
	if got := a.pollNotifications(prime, false); len(got) != 0 {
		t.Fatalf("prime poll emitted %d, want 0", len(got))
	}

	// Later poll: the old torrent is still seeding (no re-notify),
	// and a NEW torrent has just transitioned into seeding.
	later := []engine.TorrentSnapshot{
		{InfoHash: "aa", Name: "already", Status: "seeding"},
		{InfoHash: "bb", Name: "fresh-complete", Status: "seeding"},
	}
	got := a.pollNotifications(later, true)
	if len(got) != 1 {
		t.Fatalf("transition poll emitted %d notifications, want 1", len(got))
	}
	if got[0].Content != "fresh-complete" {
		t.Fatalf("notified for %q, want fresh-complete", got[0].Content)
	}

	// A subsequent identical poll must NOT re-notify.
	again := a.pollNotifications(later, true)
	if len(again) != 0 {
		t.Fatalf("repeat poll emitted %d notifications, want 0 (duplicate suppression broken)", len(again))
	}
}
