package swarmsearch

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// LocalHit is the narrow view of a hit the Protocol needs to answer an
// inbound sn_search query. It mirrors the shape of indexer.SearchHit
// with only the fields we serialise onto the wire — so the swarmsearch
// package does not need to import internal/indexer.
type LocalHit struct {
	DocType   string // "torrent" or "content"
	InfoHash  string // 40-char lowercase hex
	Name      string
	SizeBytes int64
	AddedAt   time.Time
	Seeders   int
	Leechers  int
	// Content-level fields (only used when DocType == "content").
	FileIndex int
	FilePath  string
	Score     float64
}

// LocalSearcher is the interface the Protocol uses to run queries
// against the local Bleve index. Engine wires indexer.Index up to
// this via a thin adapter so swarmsearch does not depend on indexer.
type LocalSearcher interface {
	SearchLocal(query string, limit int) (total int, hits []LocalHit, err error)
}

// ReplyFunc sends a bencoded sn_search payload back to the peer that
// originated the current inbound message. It is supplied per-call by
// the engine's PeerConnReadExtensionMessage callback, which has the
// *torrent.PeerConn in hand and can trivially wrap WriteExtendedMessage
// in a closure.
//
// Using a closure (instead of an address-keyed Sender) for inbound
// replies avoids the roundtrip through the engine's peer map and keeps
// the reply bound to the exact connection the query arrived on, which
// is what peers expect.
type ReplyFunc func(payload []byte) error

// Sender abstracts "send a bencoded sn_search payload to a specific
// peer by address." Used for OUTBOUND queries in M3c where the
// querier picks from the set of known search-capable peers. The real
// implementation (engine's peer map + WriteExtendedMessage) lives in
// internal/engine.
type Sender interface {
	Send(peerAddr string, payload []byte) error
}

// SetSearcher attaches a LocalSearcher to the Protocol. Inbound
// queries are answered against it. Pass nil to disable inbound
// handling entirely; queries will then be rejected with
// RejectShuttingDown, which is the correct shape for "this node is
// not serving queries right now."
func (p *Protocol) SetSearcher(s LocalSearcher) {
	p.mu.Lock()
	p.searcher = s
	p.mu.Unlock()
}

// SetSender attaches a Sender for outbound queries. M3b does not use
// it; M3c's Query() method does.
func (p *Protocol) SetSender(s Sender) {
	p.mu.Lock()
	p.sender = s
	p.mu.Unlock()
}

// HandleMessage is the dispatch entry point for an inbound sn_search
// payload. The engine calls it from the PeerConnReadExtensionMessage
// callback with the originating peer's address and a reply closure
// that writes back to that same connection.
//
// For M3b we handle:
//   - msg_type 0 (query) → run local search + send a Result reply via reply().
//   - msg_type 1 (result) → log-only; M3c adds the pending-query match.
//   - msg_type 2 (reject) → logged, same story.
//   - msg_type 3 (peer_announce) → logged only; M4 adds real handling.
func (p *Protocol) HandleMessage(peerAddr string, payload []byte, reply ReplyFunc) {
	// M15c: banned peers don't get any handler attention at all.
	// The ban check is cheap — one map lookup — so it's fine to
	// run on every inbound message.
	if p.misbehavior != nil && p.misbehavior.IsBanned(peerAddr) {
		p.log.Debug("swarmsearch.ban_drop", "peer", peerAddr)
		return
	}

	hdr, err := peekHeader(payload)
	if err != nil {
		p.log.Debug("swarmsearch.handle.bad_header", "peer", peerAddr, "err", err)
		// Malformed bencode is a serious misbehavior signal.
		p.chargeMisbehavior(peerAddr, ScoreBadBencode, "bad_header")
		return
	}

	switch hdr.MsgType {
	case MsgTypeQuery:
		p.handleQuery(peerAddr, payload, reply)
	case MsgTypeResult:
		res, err := DecodeResult(payload)
		if err != nil {
			p.log.Debug("swarmsearch.rx_result.decode_err",
				"peer", peerAddr, "err", err)
			p.chargeMisbehavior(peerAddr, ScoreBadBencode, "bad_result")
			return
		}
		p.routeResult(peerAddr, res)
	case MsgTypeReject:
		rj, err := DecodeReject(payload)
		if err != nil {
			p.log.Debug("swarmsearch.rx_reject.decode_err",
				"peer", peerAddr, "err", err)
			p.chargeMisbehavior(peerAddr, ScoreBadBencode, "bad_reject")
			return
		}
		p.routeReject(peerAddr, rj)
	case MsgTypePeerAnnounce:
		pa, err := DecodePeerAnnounce(payload)
		if err != nil {
			p.log.Debug("swarmsearch.rx_peer_announce.decode_err",
				"peer", peerAddr, "err", err)
			p.chargeMisbehavior(peerAddr, ScoreBadBencode, "bad_peer_announce")
			return
		}
		// Wire-compat §8.4-C: if the peer gossiped a 32-byte
		// publisher pubkey, stash it on their PeerState AND
		// forward it to any attached IndexerSink so the DHT
		// lookup layer picks it up for future fan-out. The
		// all-zero pubkey is rejected because it cannot
		// correspond to a real ed25519 identity — accepting it
		// would let a misbehaving peer silently add a useless
		// key to the indexer fan-out set on every reconnect.
		var (
			gotPubkey [32]byte
			havePk    bool
			sink      IndexerSink
		)
		if len(pa.Pubkey) == 32 {
			copy(gotPubkey[:], pa.Pubkey)
			if gotPubkey != ([32]byte{}) {
				havePk = true
			}
		}
		p.mu.Lock()
		if ps, ok := p.peers[peerAddr]; ok {
			ps.Services = ServiceBits(pa.Services)
			ps.Version = pa.Version
			if havePk {
				ps.PublisherPubkey = gotPubkey
			}
		}
		sink = p.indexerSink
		p.mu.Unlock()
		if havePk && sink != nil {
			// Label the indexer with the peer's address so ops
			// logs can tell where the pubkey came from.
			sink.NoteGossipIndexer(gotPubkey, "gossip:"+peerAddr)
		}
		p.log.Info("swarmsearch.rx_peer_announce",
			"peer", peerAddr,
			"version", pa.Version,
			"services", pa.Services,
			"pk_gossiped", havePk,
		)
	case MsgTypeSyncBegin, MsgTypeSyncSymbols, MsgTypeSyncNeed,
		MsgTypeSyncRecords, MsgTypeSyncEnd:
		p.handleSyncFrame(peerAddr, hdr, payload, reply)
	default:
		p.log.Debug("swarmsearch.unknown_msg_type",
			"peer", peerAddr, "msg_type", hdr.MsgType)
		p.chargeMisbehavior(peerAddr, ScoreUnexpectedMessage, "unknown_msg_type")
	}
}

// handleSyncFrame is the dispatch hub for Aggregate sync-session
// messages (msg_types 4..8 per SPEC §2). Every frame first clears
// the capability gate — a peer that hasn't advertised
// BitSetReconciliation in its peer_announce gets reject code 2
// and no state changes. Per-peer session state is keyed by
// (peerAddr, txid) so one peer can run multiple sessions
// sequentially; concurrent sessions on the same peer are not yet
// supported (SPEC §2.9).
func (p *Protocol) handleSyncFrame(peerAddr string, hdr messageHeader, payload []byte, reply ReplyFunc) {
	// Capability gate.
	p.mu.RLock()
	ps, known := p.peers[peerAddr]
	p.mu.RUnlock()
	hasCap := known && ps.Services.Has(BitSetReconciliation)
	if !hasCap {
		p.sendReject(reply, peerAddr, hdr.TxID, RejectUnsupportedScope,
			"sync_not_supported")
		p.chargeMisbehavior(peerAddr, ScoreUnexpectedMessage, "sync_without_cap")
		return
	}

	switch hdr.MsgType {
	case MsgTypeSyncBegin:
		m, err := DecodeSyncBegin(payload)
		if err != nil {
			p.chargeMisbehavior(peerAddr, ScoreBadBencode, "bad_sync_begin")
			return
		}
		p.onSyncBegin(peerAddr, m, reply)
	case MsgTypeSyncSymbols:
		m, err := DecodeSyncSymbols(payload)
		if err != nil {
			p.chargeMisbehavior(peerAddr, ScoreBadBencode, "bad_sync_symbols")
			return
		}
		p.onSyncSymbols(peerAddr, m)
	case MsgTypeSyncNeed:
		m, err := DecodeSyncNeed(payload)
		if err != nil {
			p.chargeMisbehavior(peerAddr, ScoreBadBencode, "bad_sync_need")
			return
		}
		p.onSyncNeed(peerAddr, m, reply)
	case MsgTypeSyncRecords:
		m, err := DecodeSyncRecords(payload)
		if err != nil {
			p.chargeMisbehavior(peerAddr, ScoreBadBencode, "bad_sync_records")
			return
		}
		p.onSyncRecords(peerAddr, m)
	case MsgTypeSyncEnd:
		m, err := DecodeSyncEnd(payload)
		if err != nil {
			p.chargeMisbehavior(peerAddr, ScoreBadBencode, "bad_sync_end")
			return
		}
		p.onSyncEnd(peerAddr, m)
	}
}

// onSyncBegin creates a responder session and emits a sync_end
// reply. A record-source hook isn't plumbed yet (lands with engine
// integration), so for now the node behaves as a publisher with
// zero records to share: ack the session, close it. This is
// correct wire behavior — SPEC §2 allows zero-symbol sessions to
// complete immediately.
func (p *Protocol) onSyncBegin(peerAddr string, m SyncBegin, reply ReplyFunc) {
	// Create a responder session for bookkeeping. When the engine
	// plumbs a record source in, this is where we'd load matching
	// local records.
	sess := NewSyncSession(m.TxID, RoleResponder, nil)
	if err := sess.ApplyBegin(m); err != nil {
		p.log.Debug("swarmsearch.sync_begin.apply_err", "peer", peerAddr, "err", err)
		return
	}
	p.registerSyncSession(peerAddr, sess)

	// No records source yet → immediately emit sync_end converged.
	end := sess.Finish(SyncStatusConverged)
	p.sendSyncEnd(reply, end)
	p.releaseSyncSession(peerAddr, m.TxID)
}

func (p *Protocol) onSyncSymbols(peerAddr string, m SyncSymbols) {
	sess := p.lookupSyncSession(peerAddr, m.TxID)
	if sess == nil {
		p.log.Debug("swarmsearch.sync_symbols.unknown_session",
			"peer", peerAddr, "txid", m.TxID)
		return
	}
	if err := sess.ApplySymbols(m); err != nil {
		p.log.Debug("swarmsearch.sync_symbols.apply_err",
			"peer", peerAddr, "err", err)
	}
}

func (p *Protocol) onSyncNeed(peerAddr string, m SyncNeed, reply ReplyFunc) {
	sess := p.lookupSyncSession(peerAddr, m.TxID)
	if sess == nil {
		p.log.Debug("swarmsearch.sync_need.unknown_session",
			"peer", peerAddr, "txid", m.TxID)
		return
	}
	// Without a records source we always report every requested
	// id as missing. Per SPEC §2.7 the peer tolerates `missing`
	// entries gracefully.
	missing := make([][32]byte, 0, len(m.IDs))
	for _, raw := range m.IDs {
		var id [32]byte
		copy(id[:], raw)
		missing = append(missing, id)
	}
	frame, err := sess.BuildRecordsFrame(nil, missing)
	if err != nil {
		p.log.Debug("swarmsearch.sync_need.build_err",
			"peer", peerAddr, "err", err)
		return
	}
	p.sendSyncRecords(reply, frame)
}

func (p *Protocol) onSyncRecords(peerAddr string, m SyncRecords) {
	sess := p.lookupSyncSession(peerAddr, m.TxID)
	if sess == nil {
		p.log.Debug("swarmsearch.sync_records.unknown_session",
			"peer", peerAddr, "txid", m.TxID)
		return
	}
	if _, err := sess.ApplyRecords(m); err != nil {
		p.log.Debug("swarmsearch.sync_records.apply_err",
			"peer", peerAddr, "err", err)
	}
}

func (p *Protocol) onSyncEnd(peerAddr string, m SyncEnd) {
	sess := p.lookupSyncSession(peerAddr, m.TxID)
	if sess != nil {
		_ = sess.ApplyEnd(m)
	}
	p.releaseSyncSession(peerAddr, m.TxID)
}

// sendSyncEnd serialises and forwards a SyncEnd frame.
func (p *Protocol) sendSyncEnd(reply ReplyFunc, m SyncEnd) {
	raw, err := EncodeSyncEnd(m)
	if err != nil {
		p.log.Debug("swarmsearch.send_sync_end.encode_err", "err", err)
		return
	}
	if reply != nil {
		_ = reply(raw)
	}
}

// sendSyncRecords serialises and forwards a SyncRecords frame.
func (p *Protocol) sendSyncRecords(reply ReplyFunc, m SyncRecords) {
	raw, err := EncodeSyncRecords(m)
	if err != nil {
		p.log.Debug("swarmsearch.send_sync_records.encode_err", "err", err)
		return
	}
	if reply != nil {
		_ = reply(raw)
	}
}

// registerSyncSession stores a session indexed by (peer, txid).
func (p *Protocol) registerSyncSession(peerAddr string, sess *SyncSession) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.syncSessions == nil {
		p.syncSessions = make(map[string]map[uint32]*SyncSession)
	}
	m, ok := p.syncSessions[peerAddr]
	if !ok {
		m = make(map[uint32]*SyncSession)
		p.syncSessions[peerAddr] = m
	}
	m[sess.TxID()] = sess
}

// lookupSyncSession returns the session for (peer, txid) or nil.
func (p *Protocol) lookupSyncSession(peerAddr string, txid uint32) *SyncSession {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if m, ok := p.syncSessions[peerAddr]; ok {
		return m[txid]
	}
	return nil
}

// releaseSyncSession drops the session entry. No-op if absent.
func (p *Protocol) releaseSyncSession(peerAddr string, txid uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if m, ok := p.syncSessions[peerAddr]; ok {
		delete(m, txid)
		if len(m) == 0 {
			delete(p.syncSessions, peerAddr)
		}
	}
}

// chargeMisbehavior adds `points` to the peer's misbehavior
// score and logs a Warn if this push crossed the ban threshold.
// Nil-safe — the test harness may construct a Protocol without
// a misbehavior tracker.
func (p *Protocol) chargeMisbehavior(peerAddr string, points int, reason string) {
	if p.misbehavior == nil {
		return
	}
	if p.misbehavior.Add(peerAddr, points) {
		p.log.Warn("swarmsearch.peer_banned",
			"peer", peerAddr,
			"reason", reason,
			"duration", BanDuration.String(),
		)
	}
}

// handleQuery runs an inbound search against the local index and
// sends back either a Result or a Reject via the reply closure.
// Errors that originate from the local index are turned into Reject
// messages so the client never hangs waiting for a reply to a
// server-side failure.
func (p *Protocol) handleQuery(peerAddr string, payload []byte, reply ReplyFunc) {
	q, err := DecodeQuery(payload)
	if err != nil {
		p.log.Debug("swarmsearch.query.decode_err", "peer", peerAddr, "err", err)
		return
	}

	// M12f: rate limit before we do any real work. A
	// misbehaving peer cannot get us to run a Bleve search
	// more than Burst times per token-bucket window. A nil
	// limiter (test harness) skips this entirely.
	if p.limiter != nil && !p.limiter.Allow(peerAddr) {
		// M15c: rate-limit hits are low-severity but add up.
		p.chargeMisbehavior(peerAddr, ScoreRateLimited, "rate_limited")
		p.sendReject(reply, peerAddr, q.TxID, RejectRateLimited, "rate_limited")
		return
	}

	p.mu.RLock()
	searcher := p.searcher
	caps := p.caps
	p.mu.RUnlock()

	// Respect ShareLocal = 0: not serving queries.
	if searcher == nil || caps.ShareLocal == 0 {
		p.sendReject(reply, peerAddr, q.TxID, RejectShuttingDown, "searcher_disabled")
		return
	}

	// Wire-compat §8.4-B: if the querier explicitly asked for a
	// scope level we don't support (e.g. 'c' content hits on a
	// C0 node), send RejectUnsupportedScope rather than silently
	// downgrade. An empty scope means "responder's choice" and
	// is always accepted. 'n' is always accepted too — every
	// node can serve torrent-name matches.
	if msg, ok := unsupportedScopeReason(q.Scope, caps); ok {
		p.sendReject(reply, peerAddr, q.TxID, RejectUnsupportedScope, msg)
		return
	}

	// Very short queries are almost always abuse or typos; reject
	// early rather than run a full-index search.
	if len(strings.TrimSpace(q.Q)) < 2 {
		p.chargeMisbehavior(peerAddr, ScoreQueryTooBroad, "query_too_short")
		p.sendReject(reply, peerAddr, q.TxID, RejectQueryTooBroad, "query_too_short")
		return
	}

	limit := q.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	total, hits, err := searcher.SearchLocal(q.Q, limit)
	if err != nil {
		p.log.Warn("swarmsearch.query.local_err",
			"peer", peerAddr, "txid", q.TxID, "err", err)
		p.sendReject(reply, peerAddr, q.TxID, RejectTooExpensive, "local_error")
		return
	}

	result := Result{
		TxID:  q.TxID,
		Total: total,
		Hits:  hitsToWire(hits),
	}
	payloadOut, err := EncodeResult(result)
	if err != nil {
		p.log.Warn("swarmsearch.query.encode_err",
			"peer", peerAddr, "txid", q.TxID, "err", err)
		return
	}
	if reply == nil {
		// No reply closure → decode-only mode used in unit tests.
		return
	}
	if err := reply(payloadOut); err != nil {
		p.log.Debug("swarmsearch.query.send_err",
			"peer", peerAddr, "txid", q.TxID, "err", err)
		return
	}
	p.log.Info("swarmsearch.query.answered",
		"peer", peerAddr,
		"txid", q.TxID,
		"q", q.Q,
		"total", total,
		"returned", len(hits),
	)
}

// hitsToWire converts LocalHit slices to the wire Hit form. Torrent-
// level hits become a plain (ih, n, s, sz) hit; content-level hits
// ride the same wire shape but add a Matches[] entry pointing at the
// file index/path so the querier can highlight the source.
func hitsToWire(local []LocalHit) []Hit {
	out := make([]Hit, 0, len(local))
	// Group content hits by infohash so each torrent contributes at
	// most one wire Hit with multiple Matches entries. The ordering
	// follows the first appearance of each infohash, which preserves
	// the local Bleve ranking.
	byIH := make(map[string]int) // infohash → index in out
	for _, h := range local {
		ih, err := hex.DecodeString(h.InfoHash)
		if err != nil || len(ih) != 20 {
			continue
		}
		switch h.DocType {
		case "content":
			idx, ok := byIH[h.InfoHash]
			if !ok {
				out = append(out, Hit{
					IH:   ih,
					N:    h.Name, // may be empty; that's OK
					Sz:   h.SizeBytes,
					Rank: int(h.Score * 1000),
				})
				idx = len(out) - 1
				byIH[h.InfoHash] = idx
			}
			out[idx].Matches = append(out[idx].Matches, FileMatch{
				FI: h.FileIndex,
				FP: h.FilePath,
			})
		default: // "torrent" or unknown
			if _, ok := byIH[h.InfoHash]; ok {
				// Already covered by an earlier content hit on the
				// same infohash; update name/size if we have better
				// data but don't duplicate the entry.
				idx := byIH[h.InfoHash]
				if out[idx].N == "" {
					out[idx].N = h.Name
				}
				if out[idx].Sz == 0 {
					out[idx].Sz = h.SizeBytes
				}
				continue
			}
			out = append(out, Hit{
				IH:   ih,
				N:    h.Name,
				S:    h.Seeders,
				L:    h.Leechers,
				Sz:   h.SizeBytes,
				T:    h.AddedAt.Unix(),
				Rank: int(h.Score * 1000),
			})
			byIH[h.InfoHash] = len(out) - 1
		}
	}
	return out
}

// sendReject is a one-liner helper to serialise + fire a reject
// message through the supplied reply closure. Errors encoding or
// sending are logged but never escalated to the caller — reject is
// best-effort.
func (p *Protocol) sendReject(reply ReplyFunc, peerAddr string, txid uint32, code int, reason string) {
	if reply == nil {
		return
	}
	body, err := EncodeReject(Reject{TxID: txid, Code: code, Reason: reason})
	if err != nil {
		p.log.Warn("swarmsearch.encode_reject_err", "err", err)
		return
	}
	if err := reply(body); err != nil {
		p.log.Debug("swarmsearch.send_reject_err",
			"peer", peerAddr, "err", err)
	}
}

// unsupportedScopeReason returns ("missing_f_hits"/"missing_c_hits",
// true) when scope asks for a level the responder's capabilities
// do not cover. Empty scope is treated as "responder's choice"
// and never triggers a reject. Unknown letters are ignored — the
// spec only defines 'n', 'f', 'c', and new letters added later
// must be forward-compatible.
func unsupportedScopeReason(scope string, caps Capabilities) (string, bool) {
	if scope == "" {
		return "", false
	}
	if strings.ContainsRune(scope, 'c') && caps.ContentHits == 0 {
		return "unsupported_scope_c", true
	}
	if strings.ContainsRune(scope, 'f') && caps.FileHits == 0 {
		return "unsupported_scope_f", true
	}
	return "", false
}

// _ is a compile-time assertion that LocalHit still has the
// three fields we reference when building wire-format responses.
// If someone renames a field in indexer.SearchHit (mirrored by
// LocalHit) this block fails to build — canary for schema drift.
var _ = func(h LocalHit) string {
	return fmt.Sprintf("%s:%d:%s", h.DocType, h.FileIndex, h.FilePath)
}
