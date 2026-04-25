package swarmsearch

import (
	"log/slog"
	"testing"
)

// TestHandleSyncFrameCapGateRejects — a peer that hasn't
// advertised BitSetReconciliation in its peer_announce gets
// reject code 2 and no state changes. Reaches the
// hasCap-false branch in handleSyncFrame.
func TestHandleSyncFrameCapGateRejects(t *testing.T) {
	t.Parallel()
	p := New(slog.Default())
	// No peer registered → known=false → hasCap=false.
	var replyCalled bool
	reply := func(payload []byte) error {
		replyCalled = true
		return nil
	}
	hdr := messageHeader{MsgType: MsgTypeSyncBegin, TxID: 99}
	p.handleSyncFrame("3.3.3.3:1", hdr, []byte{}, reply)
	if !replyCalled {
		t.Error("cap gate should have sent a Reject via the reply closure")
	}
}

// TestHandleSyncFrameCapGateKnownButMissingBit — peer is
// registered but its services bitfield does not have
// BitSetReconciliation. Should still reject.
func TestHandleSyncFrameCapGateKnownButMissingBit(t *testing.T) {
	t.Parallel()
	p := New(slog.Default())
	addr := "4.4.4.4:1"
	p.mu.Lock()
	p.peers[addr] = &PeerState{Services: 0} // no BitSetReconciliation
	p.mu.Unlock()
	var replyCalled bool
	reply := func(payload []byte) error {
		replyCalled = true
		return nil
	}
	p.handleSyncFrame(addr, messageHeader{MsgType: MsgTypeSyncBegin, TxID: 1}, []byte{}, reply)
	if !replyCalled {
		t.Error("missing-bit peer should still reject")
	}
}

// TestHandleSyncFrameBadBencodeAcrossMsgTypes — every sync
// msg_type's decode-error branch fires when fed garbage bytes
// instead of a valid bencoded payload. Each one charges
// misbehavior and returns silently — no reply, no panic.
func TestHandleSyncFrameBadBencodeAcrossMsgTypes(t *testing.T) {
	t.Parallel()
	p := New(slog.Default())
	addr := "5.5.5.5:1"
	// Register peer with cap so we get past the gate.
	p.mu.Lock()
	p.peers[addr] = &PeerState{Services: BitSetReconciliation}
	p.mu.Unlock()
	reply := func([]byte) error { return nil }

	for _, mt := range []int{
		MsgTypeSyncBegin,
		MsgTypeSyncSymbols,
		MsgTypeSyncNeed,
		MsgTypeSyncRecords,
		MsgTypeSyncEnd,
	} {
		// Each MsgType has a Decode call that fails for non-bencode
		// bytes; the handler charges misbehavior and returns.
		p.handleSyncFrame(addr, messageHeader{MsgType: mt, TxID: 1}, []byte("garbage"), reply)
	}
	// Should have accumulated misbehavior — five bad frames at
	// ScoreBadBencode each.
	score := p.MisbehaviorScore(addr)
	if score == 0 {
		t.Error("expected non-zero misbehavior score after 5 bad bencode frames")
	}
}

// TestHandleSyncFrameUnknownMsgType — an out-of-range msg_type
// past the sync-handler dispatch must not panic. The dispatch
// switch falls through to no-op (the outer HandleMessage layer
// is what catches truly unknown types via the chargeMisbehavior
// path). This locks the no-panic guarantee.
func TestHandleSyncFrameUnknownMsgType(t *testing.T) {
	t.Parallel()
	p := New(slog.Default())
	addr := "6.6.6.6:1"
	p.mu.Lock()
	p.peers[addr] = &PeerState{Services: BitSetReconciliation}
	p.mu.Unlock()
	reply := func([]byte) error { return nil }
	// 99 is not a valid sync msg_type. handleSyncFrame's switch
	// has no case for it, so the dispatch falls through silently.
	p.handleSyncFrame(addr, messageHeader{MsgType: 99, TxID: 1}, []byte{}, reply)
}
