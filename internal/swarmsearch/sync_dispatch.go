package swarmsearch

import (
	"time"

	"github.com/swartznet/swartznet/contracts/ltepwire"
)

// Sync session limits (frozen).
const (
	MaxSyncSessionsPerPeer = 4
	SyncSessionStaleAfter  = 2 * time.Minute
	syncPumpPacing         = 2 * time.Millisecond
)

// SetRecordSource / SetRecordSink / SetPublisherObserver install the record
// substrate collaborators (nil-tolerant, mutex-guarded).
func (p *Protocol) SetRecordSource(s RecordSource) { p.mu.Lock(); p.recordSource = s; p.mu.Unlock() }
func (p *Protocol) SetRecordSink(s RecordSink)     { p.mu.Lock(); p.recordSink = s; p.mu.Unlock() }
func (p *Protocol) SetPublisherObserver(o PublisherObserver) {
	p.mu.Lock()
	p.pubObserver = o
	p.mu.Unlock()
}

// ---- session registry (peerAddr, txid) ----

func (p *Protocol) registerSyncSession(addr string, s *SyncSession) {
	p.syncMu.Lock()
	defer p.syncMu.Unlock()
	if p.syncSessions[addr] == nil {
		p.syncSessions[addr] = make(map[uint32]*SyncSession)
	}
	p.syncSessions[addr][s.txid] = s
}

func (p *Protocol) lookupSyncSession(addr string, txid uint32) *SyncSession {
	p.syncMu.Lock()
	defer p.syncMu.Unlock()
	if m := p.syncSessions[addr]; m != nil {
		return m[txid]
	}
	return nil
}

func (p *Protocol) releaseSyncSession(addr string, txid uint32) {
	p.syncMu.Lock()
	defer p.syncMu.Unlock()
	if m := p.syncSessions[addr]; m != nil {
		delete(m, txid)
		if len(m) == 0 {
			delete(p.syncSessions, addr)
		}
	}
}

// releaseAllSyncSessions stops the pump and forgets EVERY sync session for a
// peer. Called on disconnect so an in-flight responder pump does not keep
// producing symbols to a dead PeerToken until the lazy reaper happens to run —
// no sync_end is sent because the peer is already gone. StopPump is idempotent.
func (p *Protocol) releaseAllSyncSessions(addr string) {
	p.syncMu.Lock()
	m := p.syncSessions[addr]
	delete(p.syncSessions, addr)
	p.syncMu.Unlock()
	for _, s := range m {
		s.StopPump()
	}
}

// registerSyncSessionIfUnderCap atomically checks the per-peer session cap and
// registers, so concurrent sync_begin frames cannot both pass a separate check
// and blow the cap (TOCTOU). A re-begin on the same txid replaces (not counted).
func (p *Protocol) registerSyncSessionIfUnderCap(addr string, s *SyncSession, cap int) bool {
	p.syncMu.Lock()
	defer p.syncMu.Unlock()
	m := p.syncSessions[addr]
	if m == nil {
		m = make(map[uint32]*SyncSession)
		p.syncSessions[addr] = m
	}
	n := 0
	for txid := range m {
		if txid != s.txid {
			n++
		}
	}
	if n >= cap {
		return false
	}
	m[s.txid] = s
	return true
}

// reapStaleSyncSessions lazily aborts sessions idle longer than
// SyncSessionStaleAfter (no background goroutine), emitting sync_end "aborted".
func (p *Protocol) reapStaleSyncSessions() {
	now := time.Now()
	type victim struct {
		addr string
		sess *SyncSession
	}
	var victims []victim
	p.syncMu.Lock()
	for addr, m := range p.syncSessions {
		for txid, s := range m {
			if s.stale(now, SyncSessionStaleAfter) {
				victims = append(victims, victim{addr, s})
				delete(m, txid)
			}
		}
		if len(m) == 0 {
			delete(p.syncSessions, addr)
		}
	}
	p.syncMu.Unlock()
	for _, v := range victims {
		v.sess.StopPump()
		if tok, ok := p.peerToken(v.addr); ok {
			if frame, err := ltepwire.EncodeSyncEnd(v.sess.Finish(ltepwire.SyncStatusAborted)); err == nil {
				p.sendVia(tok, frame)
			}
		}
	}
}

func (p *Protocol) peerToken(addr string) (PeerToken, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if ps, ok := p.peers[addr]; ok && ps.token.valid() {
		return ps.token, true
	}
	return PeerToken{}, false
}

func (p *Protocol) peerHasReconciliation(addr string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	ps, ok := p.peers[addr]
	return ok && ps.Services.Has(ltepwire.BitSetReconciliation)
}

func (p *Protocol) sendVia(token PeerToken, frame []byte) {
	p.mu.Lock()
	t := p.transport
	p.mu.Unlock()
	if t != nil {
		_ = t.SendExtension(token, frame)
	}
}

// ---- inbound dispatch (replaces the Slice-7 charge-and-reject stub) ----

// handleSyncFrame processes an inbound sync frame (msg_types 4–8). A peer that
// never advertised BitSetReconciliation gets a reject + charge; others are
// dispatched to the per-type handler.
func (p *Protocol) handleSyncFrame(peerAddr string, payload []byte, reply ReplyFunc) {
	if !p.peerHasReconciliation(peerAddr) {
		p.ban.Add(peerAddr, ScoreUnexpectedMessage)
		p.sendReject(reply, ltepwire.PeekTxID(payload), ltepwire.RejectUnsupportedScope, "sync_not_supported")
		return
	}
	p.reapStaleSyncSessions()

	mt, err := ltepwire.PeekMsgType(payload)
	if err != nil {
		p.ban.Add(peerAddr, ScoreBadBencode)
		return
	}
	switch mt {
	case ltepwire.MsgTypeSyncBegin:
		p.onSyncBegin(peerAddr, payload)
	case ltepwire.MsgTypeSyncSymbols:
		p.onSyncSymbols(peerAddr, payload)
	case ltepwire.MsgTypeSyncNeed:
		p.onSyncNeed(peerAddr, payload, reply)
	case ltepwire.MsgTypeSyncRecords:
		p.onSyncRecords(peerAddr, payload)
	case ltepwire.MsgTypeSyncEnd:
		p.onSyncEnd(peerAddr, payload)
	}
}

func (p *Protocol) onSyncBegin(peerAddr string, payload []byte) {
	m, err := ltepwire.DecodeSyncBegin(payload)
	if err != nil {
		p.ban.Add(peerAddr, ScoreBadBencode)
		return
	}
	// Opening a session is the expensive verb (record snapshot + pump); throttle
	// it on the shared per-peer token bucket so a peer cannot spin up unbounded
	// reconciliation work.
	if !p.limiter.Allow(peerAddr) {
		p.ban.Add(peerAddr, ScoreRateLimited)
		if tok, ok := p.peerToken(peerAddr); ok {
			if frame, e := ltepwire.EncodeSyncEnd(ltepwire.SyncEnd{TxID: m.TxID, Status: ltepwire.SyncStatusAborted}); e == nil {
				p.sendVia(tok, frame)
			}
		}
		return
	}
	// Snapshot the matching local records once for this session.
	var records []LocalRecord
	p.mu.Lock()
	src := p.recordSource
	p.mu.Unlock()
	if src != nil {
		if recs, e := src.LocalRecords(m.Filter); e == nil {
			records = recs
		}
	}
	sess := NewSyncSession(m.TxID, RoleResponder, records)
	if err := sess.ApplyBegin(m); err != nil {
		return
	}
	// Atomically enforce the per-peer session cap (TOCTOU-safe).
	if !p.registerSyncSessionIfUnderCap(peerAddr, sess, MaxSyncSessionsPerPeer) {
		tok, _ := p.peerToken(peerAddr)
		if frame, e := ltepwire.EncodeSyncEnd(ltepwire.SyncEnd{TxID: m.TxID, Status: ltepwire.SyncStatusAborted}); e == nil {
			p.sendVia(tok, frame)
		}
		p.ban.Add(peerAddr, ScoreUnexpectedMessage)
		return
	}

	tok, ok := p.peerToken(peerAddr)
	if !ok {
		p.releaseSyncSession(peerAddr, m.TxID)
		return
	}
	if len(records) == 0 {
		// A non-publisher converges immediately.
		if frame, e := ltepwire.EncodeSyncEnd(sess.Finish(ltepwire.SyncStatusConverged)); e == nil {
			p.sendVia(tok, frame)
		}
		p.releaseSyncSession(peerAddr, m.TxID)
		return
	}
	go p.runSyncPump(peerAddr, tok, sess)
}

// runSyncPump streams sync_symbols batches until the initiator converges
// (StopPump via sync_need), the symbol budget is exhausted (final done=1), or
// the protocol closes.
func (p *Protocol) runSyncPump(peerAddr string, tok PeerToken, sess *SyncSession) {
	for {
		select {
		case <-p.done:
			return
		case <-sess.pumpDone():
			return
		default:
		}
		frame, err := sess.ProduceSymbols(ltepwire.MaxSymbolsPerMessage)
		if err != nil {
			return // budget exhausted
		}
		done := sess.SymbolsOut() >= sess.MaxSymbols()
		frame.Done = done
		wire, encErr := ltepwire.EncodeSyncSymbols(frame)
		if encErr != nil {
			return
		}
		p.sendVia(tok, wire)
		if done {
			return
		}
		select {
		case <-p.done:
			return
		case <-sess.pumpDone():
			return
		case <-time.After(syncPumpPacing):
		}
	}
}

func (p *Protocol) onSyncSymbols(peerAddr string, payload []byte) {
	m, err := ltepwire.DecodeSyncSymbols(payload)
	if err != nil {
		p.ban.Add(peerAddr, ScoreBadBencode)
		return
	}
	sess := p.lookupSyncSession(peerAddr, m.TxID)
	if sess == nil {
		return // unknown session: silent drop
	}
	sess.mu.Lock()
	sess.touch()
	sess.mu.Unlock()
	if err := sess.ApplySymbols(m); err != nil {
		p.endWithError(peerAddr, sess, err)
		return
	}
	// If the initiator has now finalized (converged past the floor, or the
	// responder signaled done), ask for what it lacks and push what the peer
	// lacks — exactly once (the once-guard prevents duplicate needs/pushes on
	// post-convergence in-flight batches).
	if sess.role == RoleInitiator && sess.ShouldInitiatorConverge() {
		p.initiatorConverge(peerAddr, sess)
	}
}

func (p *Protocol) onSyncNeed(peerAddr string, payload []byte, reply ReplyFunc) {
	m, err := ltepwire.DecodeSyncNeed(payload)
	if err != nil {
		p.ban.Add(peerAddr, ScoreBadBencode)
		return
	}
	sess := p.lookupSyncSession(peerAddr, m.TxID)
	if sess == nil {
		return
	}
	sess.mu.Lock()
	sess.touch()
	sess.mu.Unlock()
	sess.StopPump() // the peer converged — stop streaming symbols
	found, missing, err := sess.ApplyNeed(m)
	if err != nil {
		return
	}
	send := func(wire []byte) {
		if reply != nil {
			_ = reply(wire)
		} else if tok, ok := p.peerToken(peerAddr); ok {
			p.sendVia(tok, wire)
		}
	}
	// CHUNK: a one-directional difference can need more than MaxRecordsPerMessage
	// records. A single BuildRecordsFrame(>cap) returns ErrSyncTooLarge and the
	// old code silently sent NOTHING — reconciling zero records while still
	// reporting convergence. Split into ≤cap-record frames so all records ship.
	p.sendRecordsChunked(sess, found, missing, send)
}

// sendRecordsChunked builds + sends `found` as one or more sync_records frames,
// each ≤ MaxRecordsPerMessage, so an over-cap one-directional difference
// transfers ALL records instead of none. `missing` rides the final frame; at
// least one frame is always sent (so `missing` and the phase transition ship
// even with zero records). Each frame is ingested independently by the receiver.
func (p *Protocol) sendRecordsChunked(sess *SyncSession, found []LocalRecord, missing [][32]byte, send func([]byte)) {
	chunkMax := ltepwire.MaxRecordsPerMessage
	n := len(found)
	for start := 0; start == 0 || start < n; start += chunkMax {
		end := start + chunkMax
		if end > n {
			end = n
		}
		var miss [][32]byte
		if end >= n { // the last (or only) frame carries `missing`
			miss = missing
		}
		frame, err := sess.BuildRecordsFrame(found[start:end], miss)
		if err != nil {
			return
		}
		wire, err := ltepwire.EncodeSyncRecords(frame)
		if err != nil {
			return
		}
		send(wire)
		if n == 0 {
			return
		}
	}
}

func (p *Protocol) onSyncRecords(peerAddr string, payload []byte) {
	m, err := ltepwire.DecodeSyncRecords(payload)
	if err != nil {
		p.ban.Add(peerAddr, ScoreBadBencode)
		return
	}
	sess := p.lookupSyncSession(peerAddr, m.TxID)
	if sess == nil {
		return
	}
	sess.mu.Lock()
	sess.touch()
	sess.mu.Unlock()
	recs, err := sess.ApplyRecords(m)
	if err != nil {
		p.endWithError(peerAddr, sess, err)
		return
	}
	p.ingestSyncRecords(peerAddr, recs)
}

func (p *Protocol) onSyncEnd(peerAddr string, payload []byte) {
	m, err := ltepwire.DecodeSyncEnd(payload)
	if err != nil {
		p.ban.Add(peerAddr, ScoreBadBencode)
		return
	}
	sess := p.lookupSyncSession(peerAddr, m.TxID)
	if sess == nil {
		return
	}
	sess.StopPump()
	_ = sess.ApplyEnd(m)
	p.releaseSyncSession(peerAddr, m.TxID)
}

// initiatorConverge: the initiator's decoder resolved the difference. Request
// the records it lacks and proactively push the records the peer lacks.
func (p *Protocol) initiatorConverge(peerAddr string, sess *SyncSession) {
	tok, ok := p.peerToken(peerAddr)
	if !ok {
		return
	}
	if need, err := sess.NeedFrame(sess.NeedIDs()); err == nil {
		if wire, e := ltepwire.EncodeSyncNeed(need); e == nil {
			p.sendVia(tok, wire)
		}
	}
	if push := sess.RemovedRecords(); len(push) > 0 {
		// Chunk the proactive push too — the same >MaxRecordsPerMessage silent
		// drop applied here (initiatorConverge line dropped the whole push).
		p.sendRecordsChunked(sess, push, nil, func(wire []byte) { p.sendVia(tok, wire) })
	}
}

// endWithError terminates a session: limit_exceeded (penalty-free) on a symbol/
// byte budget overrun, else aborted + a misbehavior charge.
func (p *Protocol) endWithError(peerAddr string, sess *SyncSession, err error) {
	status := ltepwire.SyncStatusAborted
	if err == ErrSymbolBudgetExceeded || err == ErrSyncBytesBudgetExceeded {
		status = ltepwire.SyncStatusLimitExceeded
	} else {
		p.ban.Add(peerAddr, ScoreUnexpectedMessage)
	}
	sess.StopPump()
	if tok, ok := p.peerToken(peerAddr); ok {
		if frame, e := ltepwire.EncodeSyncEnd(sess.Finish(status)); e == nil {
			p.sendVia(tok, frame)
		}
	}
	p.releaseSyncSession(peerAddr, sess.txid)
}

// ingestSyncRecords verifies each record's signature and absorbs the good ones,
// charging a bad signature (that record only) and observing each new publisher.
func (p *Protocol) ingestSyncRecords(peerAddr string, recs []LocalRecord) {
	p.mu.Lock()
	sink := p.recordSink
	obs := p.pubObserver
	p.mu.Unlock()
	seen := make(map[[32]byte]bool)
	for _, r := range recs {
		if err := r.Verify(); err != nil {
			p.ban.Add(peerAddr, ScoreBadRecordSig)
			continue
		}
		if sink != nil {
			sink.Add(r)
		}
		if obs != nil && !seen[r.Pk] {
			seen[r.Pk] = true
			obs.NotePublisherSeen(r.Pk)
		}
	}
}
