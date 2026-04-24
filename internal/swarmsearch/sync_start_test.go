package swarmsearch

import (
	"errors"
	"sync"
	"testing"
)

// recordingSender captures every outbound Send call for assertions.
type recordingSender struct {
	mu    sync.Mutex
	calls []senderCall
	fail  error
}

type senderCall struct {
	peer    string
	payload []byte
}

func (r *recordingSender) Send(peer string, payload []byte) error {
	if r.fail != nil {
		return r.fail
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, senderCall{peer: peer, payload: append([]byte(nil), payload...)})
	return nil
}

func (r *recordingSender) lastFor(peer string) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.calls) - 1; i >= 0; i-- {
		if r.calls[i].peer == peer {
			return r.calls[i].payload
		}
	}
	return nil
}

// StartSync refuses when the target peer is unknown.
func TestStartSyncUnknownPeer(t *testing.T) {
	p := New(nil)
	p.SetSender(&recordingSender{})
	if _, err := p.StartSync("ghost", SyncFilter{}, nil); !errors.Is(err, ErrSyncPeerUnknown) {
		t.Errorf("want ErrSyncPeerUnknown, got %v", err)
	}
}

// StartSync refuses when the peer lacks BitSetReconciliation.
func TestStartSyncPeerMissingCapability(t *testing.T) {
	p := New(nil)
	p.SetSender(&recordingSender{})
	registerPeerWithServices(t, p, "peer-no-cap", BitShareLocal) // no bit 9
	_, err := p.StartSync("peer-no-cap", SyncFilter{}, nil)
	if !errors.Is(err, ErrSyncCapabilityMissing) {
		t.Errorf("want ErrSyncCapabilityMissing, got %v", err)
	}
}

// StartSync requires an attached Sender.
func TestStartSyncNoSender(t *testing.T) {
	p := New(nil)
	// Don't SetSender — expect ErrNoSender from StartSync.
	registerPeerWithServices(t, p, "peer-1", BitSetReconciliation)
	if _, err := p.StartSync("peer-1", SyncFilter{}, nil); !errors.Is(err, ErrNoSender) {
		t.Errorf("want ErrNoSender, got %v", err)
	}
}

// Happy path: capability present, sender attached, session
// registered, sync_begin sent to the peer.
func TestStartSyncHappyPath(t *testing.T) {
	p := New(nil)
	snd := &recordingSender{}
	p.SetSender(snd)
	registerPeerWithServices(t, p, "peer-ok", BitSetReconciliation)

	sess, err := p.StartSync("peer-ok", SyncFilter{}, nil)
	if err != nil {
		t.Fatalf("StartSync: %v", err)
	}
	if sess == nil {
		t.Fatal("session nil on success")
	}
	if sess.Role() != RoleInitiator {
		t.Errorf("role = %d, want initiator", sess.Role())
	}
	if sess.Phase() != PhaseBegun {
		t.Errorf("phase = %d, want Begun", sess.Phase())
	}

	// Sender should have seen exactly one frame to the target.
	raw := snd.lastFor("peer-ok")
	if raw == nil {
		t.Fatal("sender saw no frame for peer-ok")
	}
	m, err := DecodeSyncBegin(raw)
	if err != nil {
		t.Fatalf("decode sync_begin: %v", err)
	}
	if m.TxID != sess.TxID() {
		t.Errorf("sent sync_begin txid %d != session txid %d", m.TxID, sess.TxID())
	}

	// Session is registered and retrievable by lookup.
	if got := p.lookupSyncSession("peer-ok", sess.TxID()); got != sess {
		t.Error("session not registered in p.syncSessions after StartSync")
	}
}

// If the Sender errors, the session must be released so repeat
// StartSync calls can use the same txid slot. Tests the rollback
// path.
func TestStartSyncSenderFailRollsBack(t *testing.T) {
	p := New(nil)
	snd := &recordingSender{fail: errors.New("network down")}
	p.SetSender(snd)
	registerPeerWithServices(t, p, "peer-err", BitSetReconciliation)

	if _, err := p.StartSync("peer-err", SyncFilter{}, nil); err == nil {
		t.Fatal("expected send error to propagate")
	}
	// No session should remain registered.
	p.mu.RLock()
	defer p.mu.RUnlock()
	if m, ok := p.syncSessions["peer-err"]; ok && len(m) > 0 {
		t.Errorf("session not released after send error: %d remain", len(m))
	}
}

// CloseSync emits sync_end and releases the session.
func TestCloseSyncReleasesSession(t *testing.T) {
	p := New(nil)
	snd := &recordingSender{}
	p.SetSender(snd)
	registerPeerWithServices(t, p, "peer-close", BitSetReconciliation)
	sess, _ := p.StartSync("peer-close", SyncFilter{}, nil)

	if err := p.CloseSync("peer-close", sess, SyncStatusConverged); err != nil {
		t.Fatal(err)
	}
	// Last outbound frame must be a sync_end.
	raw := snd.lastFor("peer-close")
	m, err := DecodeSyncEnd(raw)
	if err != nil {
		t.Fatalf("expected sync_end, got %v", err)
	}
	if m.TxID != sess.TxID() {
		t.Errorf("end txid mismatch")
	}
	// Session should no longer be findable.
	if got := p.lookupSyncSession("peer-close", sess.TxID()); got != nil {
		t.Error("session still registered after CloseSync")
	}
}

// SendSyncNeed must encode and ship a sync_need frame for the
// active initiator session.
func TestSendSyncNeed(t *testing.T) {
	p := New(nil)
	snd := &recordingSender{}
	p.SetSender(snd)
	registerPeerWithServices(t, p, "peer-need", BitSetReconciliation)
	sess, _ := p.StartSync("peer-need", SyncFilter{}, nil)

	var id [32]byte
	id[0] = 0xCD
	if err := p.SendSyncNeed("peer-need", sess, [][32]byte{id}); err != nil {
		t.Fatal(err)
	}
	raw := snd.lastFor("peer-need")
	m, err := DecodeSyncNeed(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.IDs) != 1 || m.IDs[0][0] != 0xCD {
		t.Errorf("sync_need IDs mismatch: %v", m.IDs)
	}
}
