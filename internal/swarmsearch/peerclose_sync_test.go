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

// TestReBeginStopsIncumbentPump is the regression for the duplicate-sync_begin
// leak: a same-txid re-begin replaces the incumbent session in the registry, and
// must StopPump the one it evicts — otherwise that pump goroutine is orphaned
// (unreachable, so never stopped) and streams to a dead token to budget
// exhaustion, bypassing the per-peer session cap.
func TestReBeginStopsIncumbentPump(t *testing.T) {
	p := New(testLog())
	const addr = "198.51.100.9:6881"

	first := NewSyncSession(5, RoleResponder, nil)
	if !p.registerSyncSessionIfUnderCap(addr, first, MaxSyncSessionsPerPeer) {
		t.Fatal("register first session")
	}
	// A second sync_begin with the SAME txid replaces `first`.
	second := NewSyncSession(5, RoleResponder, nil)
	if !p.registerSyncSessionIfUnderCap(addr, second, MaxSyncSessionsPerPeer) {
		t.Fatal("register re-begin session")
	}

	// The evicted incumbent's pump must be stopped.
	select {
	case <-first.pumpDone():
	default:
		t.Error("re-begin orphaned the incumbent pump (not stopped) — cap bypass + leak")
	}
	// The registry holds exactly the replacement (cap not blown).
	p.syncMu.Lock()
	n := len(p.syncSessions[addr])
	cur := p.syncSessions[addr][5]
	p.syncMu.Unlock()
	if n != 1 || cur != second {
		t.Errorf("registry has %d sessions (want 1, the replacement)", n)
	}
}
