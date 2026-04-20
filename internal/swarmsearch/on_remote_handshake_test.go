package swarmsearch_test

import (
	"io"
	"log/slog"
	"testing"

	pp "github.com/anacrolix/torrent/peer_protocol"
	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// TestOnRemoteHandshakeBannedPeerEarlyReturn covers the
// previously-uncovered ban-check early-return branch in
// OnRemoteHandshake. Ban the peer first via chargeMisbehavior
// (200 points crosses BanThreshold), then deliver a handshake;
// the protocol must not register the peer in its book.
func TestOnRemoteHandshakeBannedPeerEarlyReturn(t *testing.T) {
	t.Parallel()
	p := swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	const peer = "1.2.3.4:6881"

	// Drive an unknown-msg-type message through HandleMessage to
	// charge enough misbehavior to cross BanThreshold (100). One
	// 200-point ban-trigger via the badly-formed bencode path.
	p.HandleMessage(peer, []byte("not bencode"), nil) // 20 points
	p.HandleMessage(peer, []byte("not bencode"), nil) // 40
	p.HandleMessage(peer, []byte("not bencode"), nil) // 60
	p.HandleMessage(peer, []byte("not bencode"), nil) // 80
	p.HandleMessage(peer, []byte("not bencode"), nil) // 100 (crosses)
	p.HandleMessage(peer, []byte("not bencode"), nil) // 120
	if !p.IsBanned(peer) {
		t.Fatalf("peer should be banned after repeated bad-bencode (score=%d)", p.MisbehaviorScore(peer))
	}

	// A handshake claiming sn_search support; the ban check
	// rejects it before the peer is added to the book.
	hs := &pp.ExtendedHandshakeMessage{
		M: map[pp.ExtensionName]pp.ExtensionNumber{
			swarmsearch.ExtensionName: 42,
		},
	}
	p.OnRemoteHandshake(peer, hs)

	// The peer must NOT show up in either peer book table.
	for _, addr := range p.PeerBook().NewAddrs() {
		if addr == peer {
			t.Errorf("banned peer leaked into NewAddrs")
		}
	}
	for _, addr := range p.PeerBook().TriedAddrs() {
		if addr == peer {
			t.Errorf("banned peer leaked into TriedAddrs")
		}
	}
}

// TestOnRemoteHandshakeUnsupportedPeer covers the
// `supported = false` branch — a remote that handshakes without
// advertising sn_search must register a PeerState (so we know we
// saw it) but should NOT be added to the peer book (since it
// can't answer our queries).
func TestOnRemoteHandshakeUnsupportedPeer(t *testing.T) {
	t.Parallel()
	p := swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	const peer = "5.6.7.8:6881"

	hs := &pp.ExtendedHandshakeMessage{
		// No sn_search entry → unsupported.
		M: map[pp.ExtensionName]pp.ExtensionNumber{},
	}
	p.OnRemoteHandshake(peer, hs)

	for _, addr := range p.PeerBook().NewAddrs() {
		if addr == peer {
			t.Errorf("unsupported peer leaked into NewAddrs")
		}
	}
}
