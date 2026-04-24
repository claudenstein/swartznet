package swarmsearch

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
)

// captureReply records the last bencoded payload reply() saw.
// Returns the reply closure plus an accessor.
func captureReply() (ReplyFunc, func() []byte) {
	var last []byte
	r := func(b []byte) error {
		last = append([]byte(nil), b...)
		return nil
	}
	return r, func() []byte { return last }
}

// registerPeerWithServices is a test helper that injects a peer
// into the protocol's peers map with the given Services mask.
// Simulates what happens after a successful peer_announce.
func registerPeerWithServices(t *testing.T, p *Protocol, addr string, services ServiceBits) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.peers[addr] = &PeerState{
		Addr:      addr,
		Supported: true,
		Services:  services,
		SeenAt:    time.Now(),
	}
}

// A sync_begin from a peer that hasn't advertised the
// BitSetReconciliation bit receives reject code 2 and no session
// gets registered. This is the wire-compat guard — vanilla
// clients never accidentally receive sync traffic.
func TestSyncCapabilityGateRejects(t *testing.T) {
	p := New(nil)
	// Register peer WITHOUT bit 9.
	registerPeerWithServices(t, p, "peer-1", BitShareLocal|BitFileHits)

	begin := SyncBegin{TxID: 42}
	raw, err := EncodeSyncBegin(begin)
	if err != nil {
		t.Fatal(err)
	}
	reply, last := captureReply()
	p.HandleMessage("peer-1", raw, reply)

	body := last()
	if body == nil {
		t.Fatal("expected a Reject reply")
	}

	// Decode as Reject.
	var rj Reject
	if err := bencode.Unmarshal(body, &rj); err != nil {
		t.Fatalf("expected bencoded Reject, decode failed: %v", err)
	}
	if rj.MsgType != MsgTypeReject {
		t.Errorf("msg_type = %d, want Reject (%d)", rj.MsgType, MsgTypeReject)
	}
	if rj.Code != RejectUnsupportedScope {
		t.Errorf("code = %d, want UnsupportedScope (%d)", rj.Code, RejectUnsupportedScope)
	}
	if rj.TxID != 42 {
		t.Errorf("txid = %d, want 42", rj.TxID)
	}
	// No session should have been registered.
	if p.lookupSyncSession("peer-1", 42) != nil {
		t.Error("session should NOT be registered for un-capable peer")
	}
}

// A sync_begin from a capable peer is accepted; the handler
// emits a sync_end (zero-record session) and cleans up state.
func TestSyncCapableBeginReplysWithEnd(t *testing.T) {
	p := New(nil)
	registerPeerWithServices(t, p, "peer-2", BitShareLocal|BitSetReconciliation)

	begin := SyncBegin{TxID: 7}
	raw, _ := EncodeSyncBegin(begin)
	reply, last := captureReply()
	p.HandleMessage("peer-2", raw, reply)

	body := last()
	if body == nil {
		t.Fatal("expected a reply")
	}
	hdr, err := peekHeader(body)
	if err != nil {
		t.Fatal(err)
	}
	if hdr.MsgType != MsgTypeSyncEnd {
		t.Errorf("msg_type = %d, want SyncEnd (%d)", hdr.MsgType, MsgTypeSyncEnd)
	}
	end, err := DecodeSyncEnd(body)
	if err != nil {
		t.Fatal(err)
	}
	if end.Status != SyncStatusConverged {
		t.Errorf("status = %q, want converged", end.Status)
	}

	// Session should have been released after the end.
	if p.lookupSyncSession("peer-2", 7) != nil {
		t.Error("session should be released after sync_end")
	}
}

// A sync_symbols frame arriving for a txid with no active session
// is logged + dropped silently (no reply, no panic, no ban).
func TestSyncSymbolsUnknownSession(t *testing.T) {
	p := New(nil)
	registerPeerWithServices(t, p, "peer-3", BitSetReconciliation)

	syms := SyncSymbols{
		TxID:    999,
		Symbols: []SyncSymbol{{Count: 1, KeyXOR: 0xAB, DataXOR: make([]byte, 32)}},
	}
	raw, _ := EncodeSyncSymbols(syms)
	reply, last := captureReply()
	p.HandleMessage("peer-3", raw, reply)

	// No reply, no crash. The test passes by surviving.
	if body := last(); body != nil {
		t.Errorf("unexpected reply for unknown session: %x", body)
	}
}

// sync_need for an unknown session is likewise ignored; for a known
// session it replies with an all-missing sync_records frame.
func TestSyncNeedAllMissing(t *testing.T) {
	p := New(nil)
	registerPeerWithServices(t, p, "peer-4", BitSetReconciliation)

	// Manually register a session so we can exercise onSyncNeed.
	sess := NewSyncSession(1, RoleResponder, nil)
	if err := sess.ApplyBegin(SyncBegin{TxID: 1, ElementSize: 32}); err != nil {
		t.Fatal(err)
	}
	p.registerSyncSession("peer-4", sess)

	var id [32]byte
	id[0] = 0xAA
	need := SyncNeed{TxID: 1, IDs: [][]byte{id[:]}}
	raw, _ := EncodeSyncNeed(need)
	reply, last := captureReply()
	p.HandleMessage("peer-4", raw, reply)

	body := last()
	if body == nil {
		t.Fatal("expected sync_records reply")
	}
	recs, err := DecodeSyncRecords(body)
	if err != nil {
		t.Fatalf("expected sync_records, got %v (%s)", err, hex.EncodeToString(body))
	}
	if len(recs.Records) != 0 {
		t.Errorf("expected 0 records, got %d", len(recs.Records))
	}
	if len(recs.Missing) != 1 {
		t.Errorf("expected 1 missing id, got %d", len(recs.Missing))
	}
}

// Malformed sync payload charges misbehavior for bad_bencode.
func TestSyncBadPayloadCharges(t *testing.T) {
	p := New(nil)
	registerPeerWithServices(t, p, "peer-5", BitSetReconciliation)

	// Valid msg_type peek header but garbage inner payload.
	// Create a dict like {"msg_type": 4, "txid": 1} then append
	// garbage — actually the simplest way is to pass
	// a payload that peekHeader sees as sync_begin but
	// DecodeSyncBegin chokes on. We'll construct a minimal dict
	// with msg_type but missing element_size.
	bad := []byte("d8:msg_typei4e4:txidi1ee") // no algo, no element_size
	reply, _ := captureReply()

	before := p.MisbehaviorScore("peer-5")
	p.HandleMessage("peer-5", bad, reply)
	after := p.MisbehaviorScore("peer-5")

	if after <= before {
		t.Errorf("expected misbehavior score to rise after bad sync payload (%d → %d)", before, after)
	}
}

// Services bitmask Has/With/Without behaves for the new bit.
func TestBitSetReconciliationBitOps(t *testing.T) {
	s := ServiceBits(0)
	if s.Has(BitSetReconciliation) {
		t.Fatal("empty services claims BitSetReconciliation")
	}
	s = s.With(BitSetReconciliation)
	if !s.Has(BitSetReconciliation) {
		t.Error("With did not set the bit")
	}
	s = s.Without(BitSetReconciliation)
	if s.Has(BitSetReconciliation) {
		t.Error("Without did not clear the bit")
	}
}
