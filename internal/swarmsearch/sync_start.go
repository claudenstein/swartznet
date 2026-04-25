// Outbound sync initiator — the peer-side entry point that lets
// a node drive a sync session against a specific remote peer.
// SPEC.md §2.2 state machine, viewed from the initiator side.
//
// The session's responder half is handled by handler.go's
// onSync* dispatch; reply frames inbound from the peer route to
// the same *SyncSession we register here because lookupSyncSession
// is keyed on (peer, txid) regardless of role.

package swarmsearch

import (
	"errors"
	"fmt"
	"time"
)

// ErrSyncCapabilityMissing is returned when StartSync's target
// peer has not advertised BitSetReconciliation. Avoids sending
// a sync_begin that the peer would reject with code 2.
var ErrSyncCapabilityMissing = errors.New("swarmsearch: peer lacks BitSetReconciliation")

// ErrSyncPeerUnknown is returned when StartSync is asked to
// target a peer the Protocol has never seen — no entry in the
// peers map, so no services bitmask to gate on.
var ErrSyncPeerUnknown = errors.New("swarmsearch: peer not known to swarm")

// StartSync initiates an Aggregate set-reconciliation session
// against the named peer. Returns the initiator-side *SyncSession
// so callers can drive ProduceSymbols and observe NeedIDs /
// RemovedIDs / Converged as the exchange progresses.
//
// Flow:
//  1. Verify the peer advertised BitSetReconciliation — refusing
//     early is cheaper than shipping a sync_begin that gets
//     rejected with code 2.
//  2. Pick a fresh txid, construct an initiator session over
//     localRecords, call Begin(filter) to produce the sync_begin
//     frame.
//  3. Register the session with the Protocol so inbound
//     sync_symbols / sync_records / sync_end route back to it
//     via handleSyncFrame's lookup.
//  4. Encode + send the sync_begin through the attached Sender.
//  5. Return the session handle to the caller.
//
// The caller is responsible for eventually calling Finish() on
// the session and for sending a sync_end frame. A helper method
// CloseSync handles both together.
func (p *Protocol) StartSync(peerAddr string, filter SyncFilter, localRecords []LocalRecord) (*SyncSession, error) {
	// Capability gate.
	p.mu.RLock()
	ps, ok := p.peers[peerAddr]
	sender := p.sender
	p.mu.RUnlock()
	if !ok {
		return nil, ErrSyncPeerUnknown
	}
	if !ps.Services.Has(BitSetReconciliation) {
		return nil, ErrSyncCapabilityMissing
	}
	if sender == nil {
		return nil, ErrNoSender
	}

	txid := p.nextTxID()
	sess := NewSyncSession(txid, RoleInitiator, localRecords)
	beginFrame, err := sess.Begin(filter)
	if err != nil {
		return nil, fmt.Errorf("swarmsearch: build sync_begin: %w", err)
	}

	// Register BEFORE sending so an extremely fast reply (local-
	// bus transport in tests) can't lose-race the session state.
	p.registerSyncSession(peerAddr, sess)

	raw, err := EncodeSyncBegin(beginFrame)
	if err != nil {
		p.releaseSyncSession(peerAddr, txid)
		return nil, fmt.Errorf("swarmsearch: encode sync_begin: %w", err)
	}
	if err := sender.Send(peerAddr, raw); err != nil {
		p.releaseSyncSession(peerAddr, txid)
		return nil, fmt.Errorf("swarmsearch: send sync_begin: %w", err)
	}
	return sess, nil
}

// SendSyncNeed encodes and ships a sync_need frame for an active
// initiator-side session. Convenience wrapper so callers don't
// have to re-open the Sender themselves.
func (p *Protocol) SendSyncNeed(peerAddr string, sess *SyncSession, ids [][32]byte) error {
	p.mu.RLock()
	sender := p.sender
	p.mu.RUnlock()
	if sender == nil {
		return ErrNoSender
	}
	frame, err := sess.NeedFrame(ids)
	if err != nil {
		return err
	}
	raw, err := EncodeSyncNeed(frame)
	if err != nil {
		return err
	}
	return sender.Send(peerAddr, raw)
}

// CloseSync emits sync_end to the peer, marks the session
// Ended, and releases it from the Protocol's per-peer map.
// Idempotent on the lookup side — repeated calls are safe.
func (p *Protocol) CloseSync(peerAddr string, sess *SyncSession, status string) error {
	p.mu.RLock()
	sender := p.sender
	p.mu.RUnlock()
	end := sess.Finish(status)
	defer p.releaseSyncSession(peerAddr, sess.TxID())
	if sender == nil {
		return ErrNoSender
	}
	raw, err := EncodeSyncEnd(end)
	if err != nil {
		return err
	}
	return sender.Send(peerAddr, raw)
}

// WaitSyncConverged polls the session until Converged() returns
// true or timeout elapses. Utility for tests and operator tools
// that don't want to hand-roll their own convergence loop.
func (p *Protocol) WaitSyncConverged(sess *SyncSession, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if sess.Converged() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return sess.Converged()
}
