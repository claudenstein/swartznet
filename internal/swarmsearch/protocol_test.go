package swarmsearch_test

import (
	"io"
	"log/slog"
	"testing"

	pp "github.com/anacrolix/torrent/peer_protocol"

	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// fakeAdvertiser records every extension name it is asked to
// register. It is the test double for torrent.LocalLtepProtocolMap.
type fakeAdvertiser struct {
	added []pp.ExtensionName
}

func (f *fakeAdvertiser) AddUserProtocol(name pp.ExtensionName) {
	f.added = append(f.added, name)
}

func newProtocol() *swarmsearch.Protocol {
	return swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestProtocolAdvertiseOnRegistersExtensionName(t *testing.T) {
	t.Parallel()
	p := newProtocol()
	m := &fakeAdvertiser{}
	p.AdvertiseOn(m)
	if len(m.added) != 1 || m.added[0] != swarmsearch.ExtensionName {
		t.Errorf("AddUserProtocol calls = %v, want exactly [%q]",
			m.added, swarmsearch.ExtensionName)
	}
}

func TestProtocolNotePeerAdded(t *testing.T) {
	t.Parallel()
	p := newProtocol()
	p.NotePeerAdded("10.0.0.1:6881")
	p.NotePeerAdded("10.0.0.2:6881")
	peers := p.KnownPeers()
	if len(peers) != 2 {
		t.Fatalf("KnownPeers len = %d, want 2", len(peers))
	}
	if n := p.CapablePeerCount(); n != 0 {
		t.Errorf("CapablePeerCount pre-handshake = %d, want 0", n)
	}
}

func TestProtocolOnRemoteHandshakeCapable(t *testing.T) {
	t.Parallel()
	p := newProtocol()
	p.NotePeerAdded("10.0.0.1:6881")

	hs := &pp.ExtendedHandshakeMessage{
		M: map[pp.ExtensionName]pp.ExtensionNumber{
			"ut_metadata":             3,
			"ut_pex":                  2,
			swarmsearch.ExtensionName: 11,
		},
		V: "SwartzNet 0.x",
	}
	p.OnRemoteHandshake("10.0.0.1:6881", hs)

	if n := p.CapablePeerCount(); n != 1 {
		t.Errorf("CapablePeerCount = %d, want 1", n)
	}
	peers := p.KnownPeers()
	if len(peers) != 1 || !peers[0].Supported {
		t.Errorf("peer state = %+v, want Supported=true", peers)
	}
	if peers[0].RemoteExtID != 11 {
		t.Errorf("RemoteExtID = %d, want 11", peers[0].RemoteExtID)
	}
}

func TestProtocolOnRemoteHandshakeIncapable(t *testing.T) {
	t.Parallel()
	p := newProtocol()
	p.NotePeerAdded("10.0.0.2:6881")

	// A typical vanilla qBittorrent peer: no sn_search in `m`.
	hs := &pp.ExtendedHandshakeMessage{
		M: map[pp.ExtensionName]pp.ExtensionNumber{
			"ut_metadata": 3,
			"ut_pex":      2,
		},
		V: "qBittorrent/5.0",
	}
	p.OnRemoteHandshake("10.0.0.2:6881", hs)

	if n := p.CapablePeerCount(); n != 0 {
		t.Errorf("CapablePeerCount = %d, want 0 for vanilla peer", n)
	}
	peers := p.KnownPeers()
	if len(peers) != 1 || peers[0].Supported {
		t.Errorf("peer state = %+v, want Supported=false", peers)
	}
}

func TestProtocolOnPeerClosed(t *testing.T) {
	t.Parallel()
	p := newProtocol()
	p.NotePeerAdded("10.0.0.1:6881")
	p.NotePeerAdded("10.0.0.2:6881")
	p.OnPeerClosed("10.0.0.1:6881")

	peers := p.KnownPeers()
	if len(peers) != 1 {
		t.Fatalf("KnownPeers len after close = %d, want 1", len(peers))
	}
	if peers[0].Addr != "10.0.0.2:6881" {
		t.Errorf("surviving peer = %s, want 10.0.0.2:6881", peers[0].Addr)
	}
}

func TestProtocolCapabilitiesDefault(t *testing.T) {
	t.Parallel()
	p := newProtocol()
	caps := p.Capabilities()
	want := swarmsearch.DefaultCapabilities()
	if caps != want {
		t.Errorf("default caps = %+v, want %+v", caps, want)
	}
}

func TestProtocolSetCapabilities(t *testing.T) {
	t.Parallel()
	p := newProtocol()
	c := swarmsearch.Capabilities{ShareLocal: 1, FileHits: 0, ContentHits: 0, Publisher: 1}
	p.SetCapabilities(c)
	got := p.Capabilities()
	if got != c {
		t.Errorf("get = %+v, want %+v", got, c)
	}
}

// TestProtocolConcurrent exercises the lock by hammering the state
// methods from many goroutines. Run under -race to catch data races.
func TestProtocolConcurrent(t *testing.T) {
	t.Parallel()
	p := newProtocol()
	done := make(chan struct{})
	const N = 200
	for i := 0; i < N; i++ {
		go func(i int) {
			addr := "10.0.0." + string(rune('0'+(i%10))) + ":6881"
			p.NotePeerAdded(addr)
			p.OnRemoteHandshake(addr, &pp.ExtendedHandshakeMessage{
				M: map[pp.ExtensionName]pp.ExtensionNumber{
					swarmsearch.ExtensionName: 7,
				},
			})
			_ = p.KnownPeers()
			_ = p.CapablePeerCount()
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < N; i++ {
		<-done
	}
}
