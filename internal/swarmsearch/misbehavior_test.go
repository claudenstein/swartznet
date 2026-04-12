package swarmsearch

import (
	"testing"
	"time"
)

// TestMisbehaviorBasicScoring covers the Add/Score/IsBanned
// contract. Below threshold: scored but not banned. Crossing
// threshold: banned and Add returns true once.
func TestMisbehaviorBasicScoring(t *testing.T) {
	t.Parallel()
	m := newMisbehaviorTracker()
	const addr = "10.0.0.1:6881"

	if m.Score(addr) != 0 {
		t.Errorf("initial score = %d, want 0", m.Score(addr))
	}
	if m.IsBanned(addr) {
		t.Errorf("initial ban state = true, want false")
	}

	// Rack up some points below the threshold.
	banned := m.Add(addr, 30)
	if banned {
		t.Errorf("30 < BanThreshold(%d), should not ban", BanThreshold)
	}
	if s := m.Score(addr); s != 30 {
		t.Errorf("score after +30 = %d", s)
	}

	banned = m.Add(addr, 50)
	if banned {
		t.Errorf("80 < BanThreshold(%d), should not ban", BanThreshold)
	}

	// Cross the threshold.
	banned = m.Add(addr, 25) // now at 105
	if !banned {
		t.Errorf("105 >= BanThreshold(%d), should ban (returned false)", BanThreshold)
	}
	if !m.IsBanned(addr) {
		t.Errorf("IsBanned = false after crossing threshold")
	}

	// Further Adds while banned do NOT re-trigger the "just
	// banned" return (the caller only cares about the first
	// transition).
	banned = m.Add(addr, 10)
	if banned {
		t.Errorf("Add on already-banned peer returned banned=true again")
	}
}

// TestMisbehaviorBanExpiry: after BanDuration, the ban clears
// and the peer gets a fresh score budget.
func TestMisbehaviorBanExpiry(t *testing.T) {
	t.Parallel()
	m := newMisbehaviorTracker()
	const addr = "10.0.0.2:6881"

	m.Add(addr, BanThreshold)
	if !m.IsBanned(addr) {
		t.Fatalf("peer not banned after crossing threshold")
	}

	// Backdate the ban to simulate expiry.
	m.mu.Lock()
	m.peers[addr].bannedUntil = time.Now().Add(-1 * time.Hour)
	m.mu.Unlock()

	if m.IsBanned(addr) {
		t.Errorf("expired ban should auto-clear")
	}
	if s := m.Score(addr); s != 0 {
		t.Errorf("expired ban should reset score; got %d", s)
	}
}

// TestMisbehaviorForgetReleasesCleanPeers: voluntary
// disconnect of a clean peer drops the entry. Banned peers
// are retained so a reconnect hits the block.
func TestMisbehaviorForgetReleasesCleanPeers(t *testing.T) {
	t.Parallel()
	m := newMisbehaviorTracker()

	m.Add("clean:6881", 10)
	m.Add("dirty:6881", BanThreshold)

	if m.peerCount() != 2 {
		t.Fatalf("peerCount = %d, want 2", m.peerCount())
	}

	m.Forget("clean:6881")
	m.Forget("dirty:6881")

	// Clean peer is evicted, dirty (banned) peer is retained.
	if m.peerCount() != 1 {
		t.Errorf("peerCount after Forget = %d, want 1 (banned retained)", m.peerCount())
	}
	if !m.IsBanned("dirty:6881") {
		t.Errorf("banned peer ban state lost after Forget")
	}
}

// TestMisbehaviorAddZeroNoOp: Add with non-positive points
// must be a no-op so callers don't accidentally create
// entries.
func TestMisbehaviorAddZeroNoOp(t *testing.T) {
	t.Parallel()
	m := newMisbehaviorTracker()
	if m.Add("peer:1", 0) {
		t.Errorf("Add(0) returned banned=true")
	}
	if m.peerCount() != 0 {
		t.Errorf("Add(0) created an entry")
	}
	if m.Add("peer:1", -5) {
		t.Errorf("Add(-5) returned banned=true")
	}
	if m.peerCount() != 0 {
		t.Errorf("Add(-5) created an entry")
	}
}
