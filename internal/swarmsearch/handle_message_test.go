package swarmsearch_test

import (
	"io"
	"log/slog"
	"testing"

	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// TestHandleMessageBadHeader covers the peekHeader-error +
// chargeMisbehavior path.
func TestHandleMessageBadHeader(t *testing.T) {
	t.Parallel()
	p := swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	const peer = "1.2.3.4:6881"
	p.HandleMessage(peer, []byte("not bencode"), nil)
	if got := p.MisbehaviorScore(peer); got == 0 {
		t.Error("MisbehaviorScore should be non-zero after a bad-header message")
	}
}

// TestHandleMessageRouteResult exercises the MsgTypeResult dispatch
// branch with a syntactically-valid payload (no pending query =>
// dropped silently, but the decode + route path runs).
func TestHandleMessageRouteResult(t *testing.T) {
	t.Parallel()
	p := swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	body, err := swarmsearch.EncodeResult(swarmsearch.Result{TxID: 1, Hits: nil})
	if err != nil {
		t.Fatal(err)
	}
	p.HandleMessage("1.2.3.4:6881", body, nil)
}

// TestHandleMessageRouteResultBadPayload covers the
// MsgTypeResult + DecodeResult-error path. We construct a payload
// whose msg_type byte the dispatcher reads as Result but whose
// body fails the strict decoder. Since the dispatcher uses
// peekHeader before the case arm, the body must contain a valid
// msg_type field but invalid Result fields. Easiest construction:
// take a real Reject (msg_type 2 inside the bencoded body) and
// force the outer header to claim Result via... hmm, that's not
// possible because the body IS the source of msg_type.
//
// Instead drive a plausibly-constructable miss: the strict
// DecodeResult enforces msg_type == MsgTypeResult, but we already
// pass a Result-msg_type body through HandleMessage; the only
// way to fail DecodeResult here is a corrupted bencoded body.
// We cheat by hand-crafting a payload that bencodes a dict whose
// msg_type field is the Result constant but whose 'hits' field is
// the wrong type (not a list). bencode.Unmarshal then fails.
func TestHandleMessageRouteResultBadPayload(t *testing.T) {
	t.Parallel()
	p := swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	const peer = "1.2.3.4:6881"

	// d8:msg_typei1e4:hits3:badeu —
	//   d                    bencode dict start
	//   8:msg_type           key (8-byte string "msg_type")
	//   i1e                  int 1 (MsgTypeResult)
	//   4:hits               key (4-byte string "hits")
	//   3:bad                value (3-byte string) — should be a list
	//   e                    end dict
	bad := []byte("d8:msg_typei1e4:hits3:bade")
	p.HandleMessage(peer, bad, nil)
	if got := p.MisbehaviorScore(peer); got == 0 {
		t.Error("MisbehaviorScore should be non-zero after a bad-result decode")
	}
}

// TestHandleMessagePeerAnnouncePopulatesPeer covers the
// MsgTypePeerAnnounce dispatch branch. Without a PeerState
// pre-registered for the addr, the inner if-let-ok is false and
// the function falls through to the log; the dispatch line is
// covered either way.
func TestHandleMessagePeerAnnouncePopulatesPeer(t *testing.T) {
	t.Parallel()
	p := swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	body, err := swarmsearch.EncodePeerAnnounce(swarmsearch.PeerAnnounce{
		Version: 1, Services: 0xff,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Should not panic, no misbehavior score charged.
	p.HandleMessage("1.2.3.4:6881", body, nil)
	if got := p.MisbehaviorScore("1.2.3.4:6881"); got != 0 {
		t.Errorf("MisbehaviorScore = %d after a clean announce, want 0", got)
	}
}

// TestHandleMessageUnknownMsgType drives the default-case branch:
// a syntactically-valid bencoded dict whose msg_type is outside
// the recognised set must be charged as misbehavior.
func TestHandleMessageUnknownMsgType(t *testing.T) {
	t.Parallel()
	p := swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	const peer = "1.2.3.4:6881"
	// d8:msg_typei42ee — dict with msg_type=42, no other fields.
	bad := []byte("d8:msg_typei42ee")
	p.HandleMessage(peer, bad, nil)
	if got := p.MisbehaviorScore(peer); got == 0 {
		t.Error("MisbehaviorScore should be non-zero after an unknown-msg_type message")
	}
}
