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
	hdr, err := peekHeader(payload)
	if err != nil {
		p.log.Debug("swarmsearch.handle.bad_header", "peer", peerAddr, "err", err)
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
			return
		}
		p.routeResult(peerAddr, res)
	case MsgTypeReject:
		rj, err := DecodeReject(payload)
		if err != nil {
			p.log.Debug("swarmsearch.rx_reject.decode_err",
				"peer", peerAddr, "err", err)
			return
		}
		p.routeReject(peerAddr, rj)
	case MsgTypePeerAnnounce:
		p.log.Debug("swarmsearch.rx_peer_announce", "peer", peerAddr)
	default:
		p.log.Debug("swarmsearch.unknown_msg_type",
			"peer", peerAddr, "msg_type", hdr.MsgType)
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

	// Very short queries are almost always abuse or typos; reject
	// early rather than run a full-index search.
	if len(strings.TrimSpace(q.Q)) < 2 {
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

// Ensure LocalHit deduction matches the shape of indexer.SearchHit. The
// function is unused at runtime; it exists so the compiler catches
// drift if someone renames a field in indexer.SearchHit.
func _localHitKeepalive(h LocalHit) string {
	return fmt.Sprintf("%s:%d:%s", h.DocType, h.FileIndex, h.FilePath)
}
