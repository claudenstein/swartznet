package swarmsearch

import (
	"testing"
)

// TestOnPeerClosedReleasesSyncSessions is the regression for the sync-session
// leak: on disconnect, OnPeerClosed must release the peer's in-flight sync
// sessions and stop their pumps, instead of leaving them to run to budget
// exhaustion against a dead peer and linger until the lazy reaper.
func TestOnPeerClosedReleasesSyncSessions(t *testing.T) {
	p := New(testLog())
	const addr = "203.0.113.7:6881"

	s := NewSyncSession(7, RoleResponder, nil)
	if !p.registerSyncSessionIfUnderCap(addr, s, MaxSyncSessionsPerPeer) {
		t.Fatal("failed to register the session")
	}
	p.syncMu.Lock()
	n := len(p.syncSessions[addr])
	p.syncMu.Unlock()
	if n != 1 {
		t.Fatalf("registered sessions = %d, want 1", n)
	}

	p.OnPeerClosed(addr)

	// The peer's session map is gone (released).
	p.syncMu.Lock()
	_, ok := p.syncSessions[addr]
	p.syncMu.Unlock()
	if ok {
		t.Error("sync sessions not released on peer close (leak)")
	}

	// The pump was stopped (stopPumpCh closed).
	select {
	case <-s.pumpDone():
	default:
		t.Error("StopPump was not called on peer close — the pump would run to a dead peer")
	}
}
