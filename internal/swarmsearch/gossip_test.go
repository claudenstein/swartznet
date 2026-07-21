package swarmsearch

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/swartznet/swartznet/contracts/ltepwire"
)

type fakePeerSink struct {
	mu  sync.Mutex
	got []string
}

func (f *fakePeerSink) AddDiscoveredPeers(addrs []string) {
	f.mu.Lock()
	f.got = append(f.got, addrs...)
	f.mu.Unlock()
}
func (f *fakePeerSink) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.got...)
}

// frameRecorder records every outbound frame (satisfies Transport).
type frameRecorder struct {
	mu     sync.Mutex
	frames [][]byte
}

func (r *frameRecorder) SendExtension(_ PeerToken, frame []byte) error {
	r.mu.Lock()
	r.frames = append(r.frames, append([]byte(nil), frame...))
	r.mu.Unlock()
	return nil
}
func (r *frameRecorder) snPeers() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out [][]byte
	for _, f := range r.frames {
		if mt, _ := ltepwire.PeekMsgType(f); mt == ltepwire.MsgTypeSnPeers {
			out = append(out, f)
		}
	}
	return out
}

// injectGossipPeer registers a supported peer and marks it BitPeerGossip-capable.
func injectGossipPeer(p *Protocol, addr string) PeerToken {
	p.NotePeerAdded(addr)
	p.OnRemoteHandshake(addr, true, 1)
	p.mu.Lock()
	ps := p.peers[addr]
	ps.Services = ltepwire.BitPeerGossip
	tok := ps.token
	p.mu.Unlock()
	return tok
}

func cAddr(ip string, port uint16) ltepwire.CompactAddr {
	return ltepwire.CompactAddr{IP: net.ParseIP(ip).To4(), Port: port}
}

// TestHandleSnPeersConsentAndFilter: gossip is accepted only from a peer that
// advertised BitPeerGossip, and only public addresses reach the sink.
func TestHandleSnPeersConsentAndFilter(t *testing.T) {
	p := New(testLog())
	defer p.Close()
	sink := &fakePeerSink{}
	p.SetPeerSink(sink)

	// A peer that did NOT advertise gossip → dropped (and charged).
	p.NotePeerAdded("9.9.9.9:1")
	p.OnRemoteHandshake("9.9.9.9:1", true, 1)
	frame, _ := ltepwire.EncodeSnPeers([]ltepwire.CompactAddr{cAddr("1.2.3.4", 6881)})
	p.handleSnPeers("9.9.9.9:1", frame)
	if len(sink.snapshot()) != 0 {
		t.Fatal("gossip from a non-opted-in peer must be dropped")
	}

	// A peer that advertised gossip → public kept, loopback/private filtered.
	injectGossipPeer(p, "8.8.8.8:2")
	mixed, _ := ltepwire.EncodeSnPeers([]ltepwire.CompactAddr{
		cAddr("1.2.3.4", 6881),   // public → kept
		cAddr("127.0.0.1", 6881), // loopback → dropped
		cAddr("10.0.0.1", 6881),  // private → dropped
	})
	p.handleSnPeers("8.8.8.8:2", mixed)
	got := sink.snapshot()
	if len(got) != 1 || got[0] != "1.2.3.4:6881" {
		t.Fatalf("sink got %v, want just [1.2.3.4:6881]", got)
	}
}

func TestHandleSnPeersMalformed(t *testing.T) {
	p := New(testLog())
	defer p.Close()
	sink := &fakePeerSink{}
	p.SetPeerSink(sink)
	injectGossipPeer(p, "8.8.8.8:2")
	p.handleSnPeers("8.8.8.8:2", []byte("not-bencode"))
	if len(sink.snapshot()) != 0 {
		t.Error("malformed sn_peers must not reach the sink")
	}
}

// TestSendGossipConsentAndExclusion: a gossip to B carries only OTHER public,
// gossip-capable peers — not B itself, not private peers, not non-gossip peers.
func TestSendGossipConsentAndExclusion(t *testing.T) {
	p := New(testLog())
	defer p.Close()
	rt := &frameRecorder{}
	p.SetTransport(rt)

	tokB := injectGossipPeer(p, "8.8.8.8:6881") // recipient
	injectGossipPeer(p, "1.2.3.4:6881")         // C: public gossip peer → included
	injectGossipPeer(p, "10.0.0.9:6881")        // D: private → filtered out
	// E: supported but never advertised the gossip bit → excluded.
	p.NotePeerAdded("5.6.7.8:6881")
	p.OnRemoteHandshake("5.6.7.8:6881", true, 1)

	p.sendGossip(tokB)
	frames := rt.snPeers()
	if len(frames) != 1 {
		t.Fatalf("want exactly 1 sn_peers frame, got %d", len(frames))
	}
	addrs, err := ltepwire.DecodeSnPeers(frames[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(addrs) != 1 || net.IP(addrs[0].IP).String() != "1.2.3.4" {
		t.Fatalf("gossip payload = %v, want just C (1.2.3.4)", addrs)
	}
}

// TestPeerAnnounceTriggersGossip exercises the full trigger: a peer_announce
// advertising BitPeerGossip causes a one-shot sn_peers introduction to be sent.
func TestPeerAnnounceTriggersGossip(t *testing.T) {
	p := New(testLog())
	defer p.Close()
	rt := &frameRecorder{}
	p.SetTransport(rt)
	injectGossipPeer(p, "1.2.3.4:6881") // a peer to introduce

	// B connects and advertises gossip via a peer_announce.
	p.NotePeerAdded("8.8.8.8:6881")
	p.OnRemoteHandshake("8.8.8.8:6881", true, 1)
	pa, _ := ltepwire.EncodePeerAnnounce(ltepwire.PeerAnnounce{Services: uint64(ltepwire.BitPeerGossip)})
	p.HandleMessage("8.8.8.8:6881", pa, nil)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(rt.snPeers()) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	frames := rt.snPeers()
	if len(frames) == 0 {
		t.Fatal("a gossip-bit peer_announce did not trigger an sn_peers send")
	}
	addrs, _ := ltepwire.DecodeSnPeers(frames[len(frames)-1])
	if len(addrs) != 1 || net.IP(addrs[0].IP).String() != "1.2.3.4" {
		t.Fatalf("triggered gossip payload = %v, want [1.2.3.4]", addrs)
	}
}

func TestIsPublicIP(t *testing.T) {
	cases := map[string]bool{
		"1.2.3.4":              true,
		"8.8.8.8":              true,
		"2001:4860:4860::8888": true,
		"127.0.0.1":            false,
		"10.0.0.1":             false,
		"192.168.1.1":          false,
		"172.16.0.1":           false,
		"169.254.1.1":          false, // link-local
		"224.0.0.1":            false, // multicast
		"0.0.0.0":              false, // unspecified
		"::1":                  false, // v6 loopback
		"fc00::1":              false, // v6 ULA (private)
	}
	for s, want := range cases {
		if got := isPublicIP(net.ParseIP(s)); got != want {
			t.Errorf("isPublicIP(%s) = %v, want %v", s, got, want)
		}
	}
	if isPublicIP(nil) {
		t.Error("isPublicIP(nil) should be false")
	}
}
