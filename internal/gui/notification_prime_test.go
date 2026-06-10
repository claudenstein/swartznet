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

// TestPollNotificationsPrunesRemovedTorrents proves lastNotified
// cannot grow without bound: entries for torrents that have left
// the snapshot list (removed by the user) are dropped on the next
// poll instead of accumulating across long remove/re-add sessions.
func TestPollNotificationsPrunesRemovedTorrents(t *testing.T) {
	t.Parallel()

	a := &App{lastNotified: make(map[string]bool)}

	// Prime with two seeding torrents.
	prime := []engine.TorrentSnapshot{
		{InfoHash: "aa", Name: "keep", Status: "seeding"},
		{InfoHash: "bb", Name: "doomed", Status: "seeding"},
	}
	a.pollNotifications(prime, false)
	if !a.lastNotified["aa"] || !a.lastNotified["bb"] {
		t.Fatalf("prime did not record both torrents: %v", a.lastNotified)
	}

	// "bb" gets removed; the next poll must prune its entry.
	later := []engine.TorrentSnapshot{
		{InfoHash: "aa", Name: "keep", Status: "seeding"},
	}
	a.pollNotifications(later, true)
	if a.lastNotified["bb"] {
		t.Fatal("removed torrent still tracked in lastNotified (unbounded-growth regression)")
	}
	if !a.lastNotified["aa"] {
		t.Fatal("still-present torrent was wrongly pruned from lastNotified")
	}
	if len(a.lastNotified) != 1 {
		t.Fatalf("lastNotified has %d entries, want 1", len(a.lastNotified))
	}
}
