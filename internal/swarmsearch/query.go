package swarmsearch

import (
	"context"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/swartznet/swartznet/contracts/ltepwire"
)

// pendingGrace keeps a completed query's pending entry (and its asked set)
// around briefly after collection so an honest asked peer replying just after
// the deadline is matched to the query and dropped silently — NOT charged
// ScoreStaleTxID as if it were a spoofer.
const pendingGrace = 10 * time.Second

// Errors returned by Query for a request that never reaches the wire. These are
// surfaced INLINE at the HTTP boundary (a 200 response with a swarm.error
// string, never a 500 — §5.9).
var (
	ErrEmptyQuery     = errors.New("swarmsearch: empty query")
	ErrNoSender       = errors.New("swarmsearch: no transport configured")
	ErrNoCapablePeers = errors.New("swarmsearch: no sn_search-capable peers")
)

// QueryRequest is an outbound swarm search.
type QueryRequest struct {
	Q     string
	Scope string
	Limit int
}

// MergedHit is one deduplicated result across all responding peers.
type MergedHit struct {
	InfoHash string
	Name     string
	Size     int64
	Seeders  int
	Score    int      // sum of per-peer ranks, capped at 1000
	Sources  []string // peer addrs that returned this infohash
	Matches  []ltepwire.FileMatch
}

// QueryResponse is Layer S's native response (never merged with Layer L).
type QueryResponse struct {
	TxID      uint32
	Hits      []MergedHit
	Asked     int // peers we sent the query to
	Responded int // peers that returned a (non-reject) result
	Rejected  int // peers that returned a reject
}

type incomingResult struct {
	fromAddr string
	reject   bool
	code     int
	total    int
	hits     []ltepwire.Hit
}

type pendingQuery struct {
	txid    uint32
	asked   map[string]bool // immutable after registration
	results chan incomingResult

	// respMu guards responded: each asked peer may contribute at most ONE
	// frame, so a single peer cannot flood the results channel, end collection
	// early, or inflate Responded.
	respMu    sync.Mutex
	responded map[string]bool
}

// firstFrom reports whether addr is answering for the FIRST time (and records
// it). A repeat frame from an already-answered peer returns false.
func (pq *pendingQuery) firstFrom(addr string) bool {
	pq.respMu.Lock()
	defer pq.respMu.Unlock()
	if pq.responded[addr] {
		return false
	}
	pq.responded[addr] = true
	return true
}

func (p *Protocol) registerPending(pq *pendingQuery) {
	p.pendingMu.Lock()
	p.pending[pq.txid] = pq
	p.pendingMu.Unlock()
}
func (p *Protocol) lookupPending(txid uint32) *pendingQuery {
	p.pendingMu.Lock()
	defer p.pendingMu.Unlock()
	return p.pending[txid]
}
func (p *Protocol) unregisterPending(txid uint32) {
	p.pendingMu.Lock()
	delete(p.pending, txid)
	p.pendingMu.Unlock()
}

// supportedTargets snapshots the addr+token of every sn_search-capable peer.
// (Slice 7 fans out to all supported peers; the AddrMan peer book + feeler are
// a deferred local optimization — see DECISIONS S7.)
func (p *Protocol) supportedTargets() []PeerToken {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]PeerToken, 0, len(p.peers))
	for _, ps := range p.peers {
		if ps.Supported && ps.token.valid() {
			out = append(out, ps.token)
		}
	}
	return out
}

// Query fans an sn_search query out to every capable peer, collects results
// until all asked peers answer or the context deadline fires, and merges them.
func (p *Protocol) Query(ctx context.Context, req QueryRequest) (*QueryResponse, error) {
	if req.Q == "" {
		return nil, ErrEmptyQuery
	}
	p.mu.Lock()
	t := p.transport
	p.mu.Unlock()
	if t == nil {
		return nil, ErrNoSender
	}
	targets := p.supportedTargets()
	if len(targets) == 0 {
		return nil, ErrNoCapablePeers
	}

	txid := p.nextTxID()
	asked := make(map[string]bool, len(targets))
	for _, tok := range targets {
		asked[tok.Addr()] = true
	}
	pq := &pendingQuery{
		txid:      txid,
		asked:     asked,
		results:   make(chan incomingResult, len(targets)),
		responded: make(map[string]bool, len(targets)),
	}
	// Register BEFORE sending so a fast reply cannot race the registration.
	p.registerPending(pq)
	// Keep the pending alive for a grace window after Query returns so a
	// slightly-late honest reply is matched (and dropped) instead of charged.
	defer func() { time.AfterFunc(pendingGrace, func() { p.unregisterPending(txid) }) }()

	frame, err := ltepwire.EncodeQuery(ltepwire.Query{TxID: txid, Q: req.Q, Scope: req.Scope, Limit: req.Limit})
	if err != nil {
		return nil, err
	}
	sent := 0
	for _, tok := range targets {
		if err := t.SendExtension(tok, frame); err != nil {
			p.log.Debug("swarmsearch.query_send_err", "addr", tok.Addr(), "err", err)
			continue
		}
		sent++
	}
	if sent == 0 {
		return nil, ErrNoCapablePeers
	}

	resp := &QueryResponse{TxID: txid, Asked: sent}
	var collected []incomingResult
	answered := 0
	for answered < sent {
		select {
		case <-ctx.Done():
			resp.Hits = mergeResponses(collected, req.Limit)
			return resp, nil
		case in := <-pq.results:
			answered++
			if in.reject {
				resp.Rejected++
				continue
			}
			resp.Responded++
			collected = append(collected, in)
		}
	}
	resp.Hits = mergeResponses(collected, req.Limit)
	return resp, nil
}

// routeResult applies the asked-set anti-spoof, charging misbehavior before the
// txid lookup so guessing a live txid cannot dodge the malformed charge.
func (p *Protocol) routeResult(fromAddr string, r ltepwire.Result) {
	if resultIsMalformed(r) {
		p.ban.Add(fromAddr, ScoreMalformedResult)
		return
	}
	pend := p.lookupPending(r.TxID)
	if pend == nil {
		p.ban.Add(fromAddr, ScoreStaleTxID)
		return
	}
	if !pend.asked[fromAddr] {
		// A live txid alone never authenticates — the sender MUST be in the
		// immutable asked set (txids are guessable).
		p.ban.Add(fromAddr, ScoreUnexpectedMessage)
		return
	}
	if !pend.firstFrom(fromAddr) {
		// This peer already answered: drop the repeat (no double-count, no
		// early-termination, no charge — it may just be a benign duplicate).
		return
	}
	select {
	case pend.results <- incomingResult{fromAddr: fromAddr, total: r.Total, hits: r.Hits}:
	default:
	}
}

func (p *Protocol) routeReject(fromAddr string, rj ltepwire.Reject) {
	pend := p.lookupPending(rj.TxID)
	if pend == nil {
		p.ban.Add(fromAddr, ScoreStaleTxID)
		return
	}
	if !pend.asked[fromAddr] {
		p.ban.Add(fromAddr, ScoreUnexpectedMessage)
		return
	}
	if !pend.firstFrom(fromAddr) {
		return
	}
	select {
	case pend.results <- incomingResult{fromAddr: fromAddr, reject: true, code: rj.Code}:
	default:
	}
}

// resultIsMalformed flags a semantically bad result: a non-zero total with no
// hits, or any hit whose infohash is not exactly 20 bytes.
func resultIsMalformed(r ltepwire.Result) bool {
	if r.Total > 0 && len(r.Hits) == 0 {
		return true
	}
	for _, h := range r.Hits {
		if len(h.IH) != 20 {
			return true
		}
	}
	return false
}

// mergeResponses deduplicates hits by infohash (within and across peers),
// summing ranks (capped 1000), taking the max seeders and first non-empty
// name/size, unioning matches, and sorting by score then seeders.
func mergeResponses(results []incomingResult, limit int) []MergedHit {
	byIH := make(map[string]*MergedHit)
	var order []string
	seenPeerIH := make(map[string]bool) // dedup a peer repeating an IH
	for _, res := range results {
		for _, h := range res.hits {
			if len(h.IH) != 20 {
				continue
			}
			ihHex := hex.EncodeToString(h.IH)
			m := byIH[ihHex]
			if m == nil {
				m = &MergedHit{InfoHash: ihHex}
				byIH[ihHex] = m
				order = append(order, ihHex)
			}
			pk := res.fromAddr + "|" + ihHex
			if !seenPeerIH[pk] {
				seenPeerIH[pk] = true
				m.Score += h.Rank
				if m.Score > 1000 {
					m.Score = 1000
				}
				m.Sources = append(m.Sources, res.fromAddr)
			}
			if m.Name == "" && h.N != "" {
				m.Name = h.N
			}
			if m.Size == 0 && h.Sz != 0 {
				m.Size = h.Sz
			}
			if h.S > m.Seeders {
				m.Seeders = h.S
			}
			m.Matches = append(m.Matches, h.Matches...)
		}
	}
	out := make([]MergedHit, 0, len(order))
	for _, ih := range order {
		out = append(out, *byIH[ih])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Seeders > out[j].Seeders
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
