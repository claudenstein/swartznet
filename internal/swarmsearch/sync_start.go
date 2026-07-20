package swarmsearch

import (
	"errors"
	"time"

	"github.com/swartznet/swartznet/contracts/ltepwire"
)

// Initiator-side errors.
var (
	ErrSyncPeerUnknown       = errors.New("swarmsearch: peer not known")
	ErrSyncCapabilityMissing = errors.New("swarmsearch: peer does not support reconciliation")
)

// StartSync opens a reconciliation session with a peer: it sends sync_begin and
// returns the session. The session is registered BEFORE the frame is sent so a
// fast reply cannot race the registration.
func (p *Protocol) StartSync(peerAddr string, filter ltepwire.SyncFilter, localRecords []LocalRecord) (*SyncSession, error) {
	p.mu.Lock()
	ps, known := p.peers[peerAddr]
	var hasRecon bool
	var tok PeerToken
	if known {
		hasRecon = ps.Services.Has(ltepwire.BitSetReconciliation)
		tok = ps.token
	}
	t := p.transport
	p.mu.Unlock()

	if !known {
		return nil, ErrSyncPeerUnknown
	}
	if !hasRecon {
		return nil, ErrSyncCapabilityMissing
	}
	if t == nil || !tok.valid() {
		return nil, ErrNoSender
	}

	txid := p.nextTxID()
	sess := NewSyncSession(txid, RoleInitiator, localRecords)
	begin := sess.Begin(filter)
	p.registerSyncSession(peerAddr, sess) // before send

	frame, err := ltepwire.EncodeSyncBegin(begin)
	if err != nil {
		p.releaseSyncSession(peerAddr, txid)
		return nil, err
	}
	if err := t.SendExtension(tok, frame); err != nil {
		p.releaseSyncSession(peerAddr, txid)
		return nil, err
	}
	return sess, nil
}

// SendSyncNeed sends a sync_need for the given element ids.
func (p *Protocol) SendSyncNeed(peerAddr string, sess *SyncSession, ids [][32]byte) error {
	need, err := sess.NeedFrame(ids)
	if err != nil {
		return err
	}
	wire, err := ltepwire.EncodeSyncNeed(need)
	if err != nil {
		return err
	}
	tok, ok := p.peerToken(peerAddr)
	if !ok {
		return ErrNoSender
	}
	p.sendVia(tok, wire)
	return nil
}

// CloseSync ends a session, sending sync_end and releasing it. Idempotent.
func (p *Protocol) CloseSync(peerAddr string, sess *SyncSession, status string) error {
	defer p.releaseSyncSession(peerAddr, sess.TxID())
	sess.StopPump()
	wire, err := ltepwire.EncodeSyncEnd(sess.Finish(status))
	if err != nil {
		return err
	}
	tok, ok := p.peerToken(peerAddr)
	if !ok {
		return ErrNoSender
	}
	p.sendVia(tok, wire)
	return nil
}

// WaitSyncConverged polls until the initiator has FINALIZED (issued its
// reconciliation request/push — the floor-guarded, once-only decision) or the
// timeout elapses. It deliberately does NOT return on bare decoder convergence,
// which is trivially true on a too-short prefix and would drop a late-
// contributing element.
func (p *Protocol) WaitSyncConverged(sess *SyncSession, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if sess.Finalized() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return sess.Finalized()
}
