package engine

import (
	"net"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/swartznet/swartznet/internal/rendezvous"
)

func TestAddRendezvous(t *testing.T) {
	e := testEngine(t)
	ih := rendezvous.GlobalInfoHash()

	h, err := e.AddRendezvous(ih, false)
	if err != nil {
		t.Fatal(err)
	}
	if h == nil || !h.isRendezvous() {
		t.Fatal("handle not marked rendezvous")
	}
	if h.isIndexing() {
		t.Error("rendezvous handle must not index")
	}

	// Idempotent: a second add returns the same handle, no duplicate.
	h2, err := e.AddRendezvous(ih, false)
	if err != nil {
		t.Fatal(err)
	}
	if h2 != h {
		t.Error("AddRendezvous not idempotent")
	}

	// Hidden from the user-facing torrent list.
	for _, s := range e.TorrentSnapshots() {
		if s.InfoHash == ih.HexString() {
			t.Error("rendezvous swarm leaked into TorrentSnapshots")
		}
	}

	// Listed via the rendezvous accessor.
	got := e.RendezvousInfoHashes()
	if len(got) != 1 || got[0] != ih.HexString() {
		t.Errorf("RendezvousInfoHashes = %v, want [%s]", got, ih.HexString())
	}

	// Zero infohash rejected.
	if _, err := e.AddRendezvous(metainfo.Hash{}, false); err == nil {
		t.Error("zero infohash should error")
	}
}

func TestRemoveRendezvous(t *testing.T) {
	e := testEngine(t)
	ih := rendezvous.GlobalInfoHash()

	// No-op (nil) when not joined.
	if err := e.RemoveRendezvous(ih); err != nil {
		t.Errorf("RemoveRendezvous(absent) = %v, want nil", err)
	}
	if _, err := e.AddRendezvous(ih, false); err != nil {
		t.Fatal(err)
	}
	if err := e.RemoveRendezvous(ih); err != nil {
		t.Fatal(err)
	}
	if got := e.RendezvousInfoHashes(); len(got) != 0 {
		t.Errorf("still joined after remove: %v", got)
	}
}

// TestRemoveRendezvousRejectsNormalTorrent: the rendezvous-remove path must not
// be usable to drop a real download (defense against a mixed-up caller).
func TestRemoveRendezvousRejectsNormalTorrent(t *testing.T) {
	e := testEngine(t)
	var ih metainfo.Hash
	ih[0] = 0xAB
	if _, err := e.AddInfoHash(ih); err != nil {
		t.Fatal(err)
	}
	if err := e.RemoveRendezvous(ih); err == nil {
		t.Error("RemoveRendezvous on a normal torrent should error")
	}
	// And the normal torrent is still present (not dropped).
	if _, err := e.handleByHex(ih.HexString()); err != nil {
		t.Errorf("normal torrent was dropped by a rejected RemoveRendezvous: %v", err)
	}
}

// TestAddRendezvousPeersSkipsCommunity pins the review fix: PEX-learned peers are
// NEVER introduced to a private community swarm (dialing its secret infohash to
// an attacker would leak it in the handshake). With ONLY a community swarm
// joined, there is nowhere to introduce a gossip-learned peer → 0.
func TestAddRendezvousPeersSkipsCommunity(t *testing.T) {
	e := testEngine(t)
	ihc, ok := rendezvous.CommunityInfoHash("team-secret")
	if !ok {
		t.Fatal("community hash")
	}
	if _, err := e.AddRendezvous(ihc, true); err != nil {
		t.Fatal(err)
	}
	if n := e.AddRendezvousPeers([]string{"1.2.3.4:6881"}); n != 0 {
		t.Errorf("community-only AddRendezvousPeers = %d, want 0 (community must be excluded)", n)
	}
}

func TestAddRendezvousPeersNoSwarms(t *testing.T) {
	e := testEngine(t)
	if n := e.AddRendezvousPeers([]string{"1.2.3.4:6881"}); n != 0 {
		t.Errorf("AddRendezvousPeers with no swarms = %d, want 0", n)
	}
}

// TestAddRendezvousPeersRejectsMalformed: with a swarm present, a batch of only
// malformed/hostname addresses introduces nobody (proves the parse filter runs
// before AddPeers, independent of anacrolix's internal accept count).
func TestAddRendezvousPeersRejectsMalformed(t *testing.T) {
	e := testEngine(t)
	if _, err := e.AddRendezvous(rendezvous.GlobalInfoHash(), false); err != nil {
		t.Fatal(err)
	}
	bad := []string{"evil.example.com:80", "garbage", "1.2.3.4:70000", "1.2.3.4", ":6881"}
	if n := e.AddRendezvousPeers(bad); n != 0 {
		t.Errorf("AddRendezvousPeers(all-malformed) = %d, want 0", n)
	}
}

// TestParseNumericPeers pins the address filter directly: only numeric ip:port
// survives; hostnames (DNS risk) and bad ports are dropped.
func TestParseNumericPeers(t *testing.T) {
	in := []string{
		"1.2.3.4:6881",        // ok v4
		"[2001:db8::1]:51413", // ok v6
		"evil.example.com:80", // hostname → rejected
		"1.2.3.4",             // no port
		"1.2.3.4:0",           // port 0
		"1.2.3.4:70000",       // port out of range
		"not-an-addr",         // junk
	}
	got := parseNumericPeers(in)
	if len(got) != 2 {
		t.Fatalf("parseNumericPeers accepted %d, want 2 (the two numeric addrs)", len(got))
	}
	for _, p := range got {
		ta, ok := p.Addr.(*net.TCPAddr)
		if !ok || ta.IP == nil {
			t.Errorf("parsed peer has bad addr: %+v", p.Addr)
		}
		if p.Source != "X" { // torrent.PeerSourcePex
			t.Errorf("peer source = %q, want PeerSourcePex", p.Source)
		}
	}
}
