package swarmsearch_test

import (
	"io"
	"log/slog"
	"testing"

	pp "github.com/anacrolix/torrent/peer_protocol"
	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// TestHandleMessageRouteRejectBadPayload covers the
// MsgTypeReject + DecodeReject-error branch of HandleMessage.
// peekHeader sees msg_type=2 and dispatches to the Reject arm,
// but the bencoded body has a non-int "code" field so the
// strict DecodeReject fails. Misbehavior must be charged.
func TestHandleMessageRouteRejectBadPayload(t *testing.T) {
	t.Parallel()
	p := swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	const peer = "1.2.3.4:6881"
	// d8:msg_typei2e4:code3:bade — code field is a string, not int.
	bad := []byte("d8:msg_typei2e4:code3:bade")
	p.HandleMessage(peer, bad, nil)
	if got := p.MisbehaviorScore(peer); got == 0 {
		t.Error("MisbehaviorScore should be non-zero after a bad-reject decode")
	}
}

// TestHandleMessagePeerAnnounceBadPayload covers the
// MsgTypePeerAnnounce + DecodePeerAnnounce-error branch. The
// bencoded body has the right msg_type but the "v" field is a
// string instead of int.
func TestHandleMessagePeerAnnounceBadPayload(t *testing.T) {
	t.Parallel()
	p := swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	const peer = "5.6.7.8:6881"
	// d8:msg_typei3e1:v3:bade — v is a string, not int.
	bad := []byte("d8:msg_typei3e1:v3:bade")
	p.HandleMessage(peer, bad, nil)
	if got := p.MisbehaviorScore(peer); got == 0 {
		t.Error("MisbehaviorScore should be non-zero after a bad-peer-announce decode")
	}
}

// TestHandleMessagePeerAnnounceUpdatesRegisteredPeer covers the
// `if ps, ok := p.peers[peerAddr]; ok` branch in the
// MsgTypePeerAnnounce arm. Register a peer via OnRemoteHandshake
// first so the inner update fires (Services + Version copied
// from the announce).
func TestHandleMessagePeerAnnounceUpdatesRegisteredPeer(t *testing.T) {
	t.Parallel()
	p := swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	const peer = "9.9.9.9:6881"

	// Register a PeerState via the handshake path.
	hs := &pp.ExtendedHandshakeMessage{
		M: map[pp.ExtensionName]pp.ExtensionNumber{
			swarmsearch.ExtensionName: 42,
		},
	}
	p.OnRemoteHandshake(peer, hs)

	body, err := swarmsearch.EncodePeerAnnounce(swarmsearch.PeerAnnounce{
		Version:  3,
		Services: 0xAB,
	})
	if err != nil {
		t.Fatal(err)
	}
	p.HandleMessage(peer, body, nil)

	// No misbehavior — clean announce.
	if got := p.MisbehaviorScore(peer); got != 0 {
		t.Errorf("MisbehaviorScore = %d, want 0 after clean announce", got)
	}
}
