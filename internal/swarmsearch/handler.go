package swarmsearch

import (
	"encoding/hex"
	"strings"

	"github.com/swartznet/swartznet/contracts/ltepwire"
)

// ReplyFunc sends a reply frame back to the peer that sent the inbound message.
// It is a per-call closure bound to the exact connection, supplied by the
// engine and never stored — so a reply fires only in response to an inbound
// frame (a vanilla peer never triggers one). A nil ReplyFunc is decode-only
// (used by tests): the handler runs all logic and charges but sends nothing.
type ReplyFunc func(payload []byte) error

var zeroPubkey [32]byte

// HandleMessage processes one inbound sn_search frame from peerAddr. It is
// called by the engine on a worker goroutine OFF the anacrolix read loop (so a
// reply cannot self-deadlock on the client lock). Safe for concurrent use.
func (p *Protocol) HandleMessage(peerAddr string, payload []byte, reply ReplyFunc) {
	if p.ban.IsBanned(peerAddr) {
		return
	}
	msgType, err := ltepwire.PeekMsgType(payload)
	if err != nil {
		p.ban.Add(peerAddr, ScoreBadBencode)
		return
	}
	switch msgType {
	case ltepwire.MsgTypeQuery:
		p.handleQuery(peerAddr, payload, reply)
	case ltepwire.MsgTypeResult:
		r, err := ltepwire.DecodeResult(payload)
		if err != nil {
			p.ban.Add(peerAddr, ScoreBadBencode)
			return
		}
		p.routeResult(peerAddr, r)
	case ltepwire.MsgTypeReject:
		rj, err := ltepwire.DecodeReject(payload)
		if err != nil {
			p.ban.Add(peerAddr, ScoreBadBencode)
			return
		}
		p.routeReject(peerAddr, rj)
	case ltepwire.MsgTypePeerAnnounce:
		pa, err := ltepwire.DecodePeerAnnounce(payload)
		if err != nil {
			p.ban.Add(peerAddr, ScoreBadBencode)
			return
		}
		p.handlePeerAnnounce(peerAddr, pa)
	case ltepwire.MsgTypeSyncBegin, ltepwire.MsgTypeSyncSymbols, ltepwire.MsgTypeSyncNeed,
		ltepwire.MsgTypeSyncRecords, ltepwire.MsgTypeSyncEnd:
		// RIBLT Aggregate set-reconciliation (Slice 8). Gated on the peer
		// having advertised BitSetReconciliation.
		p.handleSyncFrame(peerAddr, payload, reply)
	case ltepwire.MsgTypeSnPeers:
		// sn_peers PEX (capable-peer discovery). Gated on the peer having
		// advertised BitPeerGossip (checked inside).
		p.handleSnPeers(peerAddr, payload)
	default:
		p.ban.Add(peerAddr, ScoreUnexpectedMessage)
	}
}

// handleQuery answers (or rejects) an inbound query. The gate order is
// load-bearing and fail-closed.
func (p *Protocol) handleQuery(addr string, payload []byte, reply ReplyFunc) {
	q, err := ltepwire.DecodeQuery(payload)
	if err != nil {
		// §6 fix: a valid-bencode wrong-shape query is CHARGED (legacy only
		// logged, leaving it free).
		p.ban.Add(addr, ScoreBadBencode)
		return
	}
	if !p.limiter.Allow(addr) {
		p.ban.Add(addr, ScoreRateLimited)
		p.sendReject(reply, q.TxID, ltepwire.RejectRateLimited, "rate_limited")
		return
	}

	p.mu.Lock()
	searcher := p.searcher
	p.mu.Unlock()
	caps := p.caps().Sharing

	// ShareLocal 0 (off) and 1 (swarm-only, no membership filter) both fail
	// CLOSED; only ShareLocal 2 (full local) serves.
	if searcher == nil || caps.ShareLocal != 2 {
		p.sendReject(reply, q.TxID, ltepwire.RejectShuttingDown, "searcher_disabled")
		return
	}
	if code, reason, rejected := unsupportedScope(q.Scope, caps); rejected {
		p.sendReject(reply, q.TxID, code, reason)
		return
	}
	if len(strings.TrimSpace(q.Q)) < 2 {
		p.ban.Add(addr, ScoreQueryTooBroad)
		p.sendReject(reply, q.TxID, ltepwire.RejectQueryTooBroad, "query_too_short")
		return
	}
	limit := q.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	total, hits, err := searcher.SearchLocal(q.Q, limit)
	if err != nil {
		p.sendReject(reply, q.TxID, ltepwire.RejectTooExpensive, "local_error")
		return
	}
	if reply == nil {
		return
	}
	frame, err := ltepwire.EncodeResult(ltepwire.Result{TxID: q.TxID, Total: total, Hits: hitsToWire(hits, caps)})
	if err != nil {
		p.log.Debug("swarmsearch.result_encode_err", "err", err)
		return
	}
	if err := reply(frame); err != nil {
		p.log.Debug("swarmsearch.reply_err", "addr", addr, "err", err)
	}
}

// unsupportedScope reports whether the query's explicit scope demands a
// capability we lack. 'c' is tested before 'f' (a node lacking both returns
// "_c" for scope "fc"). Empty scope and 'n' are always accepted; unknown
// letters ignored. This is a deliberate NON-downgrade: an explicit c/f against
// a lacking node is rejected, never silently narrowed.
func unsupportedScope(scope string, caps ltepwire.Sharing) (code int, reason string, rejected bool) {
	if strings.ContainsRune(scope, 'c') && !caps.ContentHits {
		return ltepwire.RejectUnsupportedScope, "unsupported_scope_c", true
	}
	if strings.ContainsRune(scope, 'f') && !caps.FileHits {
		return ltepwire.RejectUnsupportedScope, "unsupported_scope_f", true
	}
	return 0, "", false
}

func (p *Protocol) sendReject(reply ReplyFunc, txid uint32, code int, reason string) {
	if reply == nil {
		return
	}
	frame, err := ltepwire.EncodeReject(ltepwire.Reject{TxID: txid, Code: code, Reason: reason})
	if err != nil {
		return
	}
	_ = reply(frame)
}

// hitsToWire converts local hits to wire hits, grouping content matches under
// their torrent's single Hit (first-appearance order preserves rank). A
// non-hex / non-20-byte infohash is skipped. The freshness stamp T is emitted
// only when AddedAt is non-zero (never the year-1 stamp — §6 defect b).
//
// caps enforces the node's OWN advertised sharing policy on the RESPONSE PAYLOAD,
// not just against an explicit scope request (unsupportedScope). Otherwise a peer
// sends scope="n" (or empty) — which unsupportedScope always accepts — and still
// receives content matches + per-file paths the operator set ContentHits/FileHits
// false to withhold. Enforcement:
//   - !ContentHits: drop every content-derived match, and drop a torrent that
//     surfaced ONLY via content (revealing it leaks that its content matched).
//   - !FileHits: strip the per-file path (FP) from any match that survives.
func hitsToWire(hits []LocalHit, caps ltepwire.Sharing) []ltepwire.Hit {
	byIH := make(map[string]*ltepwire.Hit)
	hasName := make(map[string]bool)
	var order []string
	for _, h := range hits {
		ih, err := hex.DecodeString(h.InfoHash)
		if err != nil || len(ih) != 20 {
			continue
		}
		w := byIH[h.InfoHash]
		if w == nil {
			var t int64
			if !h.AddedAt.IsZero() {
				t = h.AddedAt.Unix()
			}
			w = &ltepwire.Hit{
				IH:   ih,
				N:    h.Name,
				S:    h.Seeders,
				L:    h.Leechers,
				Sz:   h.SizeBytes,
				T:    t,
				Rank: int(h.Score * 1000),
			}
			byIH[h.InfoHash] = w
			order = append(order, h.InfoHash)
		}
		if h.DocType == "content" {
			if !caps.ContentHits {
				continue // content matches withheld by policy
			}
			fm := ltepwire.FileMatch{FI: h.FileIndex}
			if caps.FileHits {
				fm.FP = h.FilePath // per-file path withheld unless FileHits
			}
			w.Matches = append(w.Matches, fm)
		} else {
			hasName[h.InfoHash] = true
		}
	}
	out := make([]ltepwire.Hit, 0, len(order))
	for _, ih := range order {
		// A torrent that surfaced ONLY via content (no name-level hit) is withheld
		// entirely when ContentHits is off — otherwise its presence leaks that the
		// withheld content matched the query.
		if !caps.ContentHits && !hasName[ih] {
			continue
		}
		out = append(out, *byIH[ih])
	}
	return out
}

// handlePeerAnnounce records the remote's advertised services/version/pubkey
// and routes gossip. An all-zero pubkey is rejected (impossible ed25519 id)
// while the rest of the frame is still processed.
func (p *Protocol) handlePeerAnnounce(addr string, pa ltepwire.PeerAnnounce) {
	var gotPubkey [32]byte
	havePk := false
	if len(pa.Pk) == 32 && [32]byte(pa.Pk) != zeroPubkey {
		copy(gotPubkey[:], pa.Pk)
		havePk = true
	}

	p.mu.Lock()
	ps := p.peers[addr]
	if ps == nil {
		// The peer is not (or no longer) connected. Do NOT create an entry here:
		// peer_announce is dispatched on an async worker, so it can run AFTER
		// OnPeerClosed already deleted this addr — and p.peers has no reaper or cap,
		// so re-creating a zombie for a dead connection is a remote-triggerable
		// unbounded memory leak (each reconnect uses a fresh ephemeral port → a
		// fresh key). Every LIVE connection already has an entry created
		// synchronously by NotePeerAdded before any message is processed, so a
		// legitimate announce always finds one; a missing entry means "gone".
		p.mu.Unlock()
		return
	}
	ps.Services = ltepwire.ServiceBits(pa.Services) // unknown bits ignored, never rejected
	ps.Version = pa.Version
	if havePk {
		ps.PublisherPubkey = gotPubkey
		ps.hasPubkey = true
	}
	// PEX: if the peer just advertised BitPeerGossip, introduce our other capable
	// peers to it (once). Enqueue is non-blocking, so it is safe under p.mu.
	p.maybeGossipLocked(ps)
	idxSink, endSink := p.idxSink, p.endSink
	p.mu.Unlock()

	if !havePk {
		return
	}
	if idxSink != nil {
		idxSink.NoteGossipIndexer(gotPubkey, "gossip:"+addr)
	}
	if endSink == nil {
		return
	}
	for _, e := range pa.Endorsed {
		if len(e) != 32 {
			continue
		}
		var cand [32]byte
		copy(cand[:], e)
		if cand == zeroPubkey || cand == gotPubkey { // no self-endorse, no zero
			continue
		}
		endSink.NoteEndorsement(gotPubkey, cand)
	}
}
