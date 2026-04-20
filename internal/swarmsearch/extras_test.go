package swarmsearch_test

import (
	"io"
	"log/slog"
	"testing"

	"github.com/swartznet/swartznet/internal/swarmsearch"
)

func TestProtocolHitCacheReturnsConfiguredCache(t *testing.T) {
	t.Parallel()
	p := swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))

	hc := p.HitCache()
	if hc == nil {
		t.Fatal("HitCache returned nil")
	}
	// Fresh cache must be empty.
	if got := hc.Size(); got != 0 {
		t.Errorf("Size on fresh cache = %d, want 0", got)
	}
}

// TestProtocolMisbehaviorScoreFreshIsZero exercises the
// misbehavior-score getter in the case where the peer has no
// recorded misbehavior — the typical /status output for a healthy
// peer set.
func TestProtocolMisbehaviorScoreFreshIsZero(t *testing.T) {
	t.Parallel()
	p := swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if got := p.MisbehaviorScore("203.0.113.1:6881"); got != 0 {
		t.Errorf("MisbehaviorScore on unknown peer = %d, want 0", got)
	}
}

func TestProtocolIsBannedFreshIsFalse(t *testing.T) {
	t.Parallel()
	p := swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if p.IsBanned("203.0.113.1:6881") {
		t.Error("IsBanned on a fresh peer should be false")
	}
}

func TestEncodeDecodePeerAnnounceRoundTrip(t *testing.T) {
	t.Parallel()
	pa := swarmsearch.PeerAnnounce{
		Version:  1,
		Services: 0xdeadbeef,
	}
	encoded, err := swarmsearch.EncodePeerAnnounce(pa)
	if err != nil {
		t.Fatalf("EncodePeerAnnounce: %v", err)
	}

	got, err := swarmsearch.DecodePeerAnnounce(encoded)
	if err != nil {
		t.Fatalf("DecodePeerAnnounce: %v", err)
	}
	if got.Version != 1 {
		t.Errorf("Version = %d, want 1", got.Version)
	}
	if got.Services != 0xdeadbeef {
		t.Errorf("Services = 0x%x, want 0xdeadbeef", got.Services)
	}
}

func TestDecodePeerAnnounceRejectsGarbage(t *testing.T) {
	t.Parallel()
	if _, err := swarmsearch.DecodePeerAnnounce([]byte("not bencode")); err == nil {
		t.Error("DecodePeerAnnounce on non-bencode should error")
	}
}

// TestDecodePeerAnnounceRejectsWrongMsgType covers the second
// guard: a syntactically-valid bencode message whose msg_type is
// not MsgTypePeerAnnounce.
func TestDecodePeerAnnounceRejectsWrongMsgType(t *testing.T) {
	t.Parallel()
	// Encode a Query (msg_type 1) instead of an announce (msg_type 3),
	// then try to decode it as an announce.
	q, err := swarmsearch.EncodeQuery(swarmsearch.Query{TxID: 1, Q: "x", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := swarmsearch.DecodePeerAnnounce(q); err == nil {
		t.Error("DecodePeerAnnounce on a wrong-msg_type payload should error")
	}
}
