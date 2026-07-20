package swarmsearch

import (
	"bytes"
	"testing"

	"github.com/swartznet/swartznet/contracts/ltepwire"
)

// TestPeerAnnounceAfterCloseDoesNotResurrect pins the round-9 fix: peer_announce
// is dispatched on an async worker and can run AFTER OnPeerClosed already dropped
// the peer. Because p.peers has no reaper or cap, re-creating an entry for a dead
// connection is a remote-triggerable unbounded memory leak. handlePeerAnnounce
// must update an EXISTING entry only, never create one.
func TestPeerAnnounceAfterCloseDoesNotResurrect(t *testing.T) {
	p := New(testLog())
	defer p.Close()
	const addr = "1.2.3.4:5678"
	pa := ltepwire.PeerAnnounce{Version: 1, Pk: bytes.Repeat([]byte{0x11}, 32)}

	// Live connection: NotePeerAdded creates the entry (as it does on every
	// PeerConnAdded, synchronously, before any message), announce updates it.
	p.NotePeerAdded(addr)
	p.handlePeerAnnounce(addr, pa)
	if got := p.KnownPeers(); got != 1 {
		t.Fatalf("live peer not tracked: KnownPeers=%d, want 1", got)
	}

	// Connection closes → entry deleted.
	p.OnPeerClosed(addr)
	if got := p.KnownPeers(); got != 0 {
		t.Fatalf("OnPeerClosed did not drop the peer: KnownPeers=%d, want 0", got)
	}

	// A late async peer_announce for the now-dead connection must NOT resurrect it.
	p.handlePeerAnnounce(addr, pa)
	if got := p.KnownPeers(); got != 0 {
		t.Errorf("peer_announce after close resurrected a zombie entry: KnownPeers=%d, want 0", got)
	}
}

// TestPeerAnnounceUpdatesLivePeer confirms the fix does not break the normal
// path: an announce on a live (NotePeerAdded) peer still records its pubkey.
func TestPeerAnnounceUpdatesLivePeer(t *testing.T) {
	p := New(testLog())
	defer p.Close()
	const addr = "9.9.9.9:1000"
	p.NotePeerAdded(addr)
	p.handlePeerAnnounce(addr, ltepwire.PeerAnnounce{Version: 1, Pk: bytes.Repeat([]byte{0x22}, 32)})

	p.mu.Lock()
	ps := p.peers[addr]
	p.mu.Unlock()
	if ps == nil || !ps.hasPubkey {
		t.Fatalf("announce did not record the pubkey on a live peer: %+v", ps)
	}
}
