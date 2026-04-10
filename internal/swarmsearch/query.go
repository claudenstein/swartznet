package swarmsearch

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// QueryRequest parameterises an outbound sn_search query. Only Q is
// required; sensible defaults kick in for the rest.
type QueryRequest struct {
	// Q is the user's query string.
	Q string
	// Scope is the subset of ["n","f","c"] the querier wants. Empty
	// means DefaultScope.
	Scope string
	// PerPeerLimit is the maximum number of hits each peer should
	// return. Zero → 50.
	PerPeerLimit int
	// Timeout is the overall wall-clock budget for the query. Zero →
	// 3 seconds.
	Timeout time.Duration
}

// QueryResponse is the merged result of a fan-out Query. It contains
// hits from every peer that responded in time, deduplicated and
// re-ranked.
type QueryResponse struct {
	// TxID is the transaction id generated for this query. Useful
	// for correlation with log output.
	TxID uint32
	// Hits are merged + re-ranked across responders.
	Hits []MergedHit
	// Asked is the number of peers we sent the query to.
	Asked int
	// Responded is the number of peers that sent a non-reject reply
	// before the timeout.
	Responded int
	// Rejected is the number of peers that replied with an explicit
	// reject.
	Rejected int
}

// MergedHit is a post-merge hit row. It deduplicates the same
// infohash across responders by summing their individual rank scores
// (capped) and attributing the hit to every peer that returned it.
type MergedHit struct {
	// InfoHash is the 40-char lowercase hex representation of the
	// underlying 20-byte id.
	InfoHash string
	// Name is the human-readable torrent name; the first non-empty
	// name across responders wins.
	Name string
	// Size is the torrent size in bytes; first non-zero wins.
	Size int64
	// Seeders is the max reported seeder count across responders.
	Seeders int
	// Score is the summed rank from all responders, capped at 1000.
	Score int
	// Matches is the union of per-file matches across responders.
	Matches []FileMatch
	// Sources is the list of peer addresses that returned this hit.
	Sources []string
}

// Errors returned by Query.
var (
	ErrNoCapablePeers = errors.New("swarmsearch: no search-capable peers known")
	ErrNoSender       = errors.New("swarmsearch: sender not configured")
	ErrEmptyQuery     = errors.New("swarmsearch: empty query")
)

// pendingQuery holds the server-side state for a Query that has been
// sent out and is waiting for responses. The inbound handler looks
// it up by txid and routes each Result into results.
type pendingQuery struct {
	txid      uint32
	results   chan incomingResult
	expected  int // number of peers we fired the query to
}

// incomingResult bundles a decoded Result with the address of the
// peer that sent it, so the merger can attribute sources correctly.
type incomingResult struct {
	peer   string
	result Result
}

// nextTxID returns a monotonically increasing transaction id per
// Protocol. Wraps at uint32 boundaries, which is fine because we
// only ever have a handful of outbound queries in flight at once.
func (p *Protocol) nextTxID() uint32 {
	return atomic.AddUint32(&p.txidCounter, 1)
}

// registerPending stores a pendingQuery under its txid so the
// result-handling branch of HandleMessage can find it. The caller is
// responsible for calling releasePending when done.
func (p *Protocol) registerPending(q *pendingQuery) {
	p.pendingMu.Lock()
	if p.pending == nil {
		p.pending = make(map[uint32]*pendingQuery)
	}
	p.pending[q.txid] = q
	p.pendingMu.Unlock()
}

// releasePending removes a pendingQuery from the registry. Safe to
// call multiple times.
func (p *Protocol) releasePending(txid uint32) {
	p.pendingMu.Lock()
	delete(p.pending, txid)
	p.pendingMu.Unlock()
}

// lookupPending fetches a pendingQuery by txid, or nil if no such
// query is currently in flight.
func (p *Protocol) lookupPending(txid uint32) *pendingQuery {
	p.pendingMu.RLock()
	defer p.pendingMu.RUnlock()
	return p.pending[txid]
}

// Query fans an sn_search query out to every known search-capable
// peer and returns a merged QueryResponse. Blocks until the
// per-query Timeout expires or every asked peer has responded
// (whichever is sooner).
//
// The caller is expected to hold a *Protocol whose Sender has been
// attached (engine.New does this automatically). In tests, SetSender
// with a fake implementation.
func (p *Protocol) Query(ctx context.Context, req QueryRequest) (*QueryResponse, error) {
	if req.Q == "" {
		return nil, ErrEmptyQuery
	}
	if req.PerPeerLimit <= 0 {
		req.PerPeerLimit = 50
	}
	if req.Timeout <= 0 {
		req.Timeout = 3 * time.Second
	}

	p.mu.RLock()
	sender := p.sender
	p.mu.RUnlock()
	if sender == nil {
		return nil, ErrNoSender
	}

	// Snapshot the capable peer set. Peers discovered after this
	// point won't receive this query — that is intentional, so the
	// result is bounded.
	peerSnap := p.KnownPeers()
	var targets []PeerState
	for _, ps := range peerSnap {
		if ps.Supported {
			targets = append(targets, ps)
		}
	}
	if len(targets) == 0 {
		return nil, ErrNoCapablePeers
	}

	txid := p.nextTxID()
	pend := &pendingQuery{
		txid:     txid,
		results:  make(chan incomingResult, len(targets)),
		expected: len(targets),
	}
	p.registerPending(pend)
	defer p.releasePending(txid)

	payload, err := EncodeQuery(Query{
		TxID:  txid,
		Q:     req.Q,
		Scope: req.Scope,
		Limit: req.PerPeerLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("swarmsearch: encode query: %w", err)
	}

	// Fire the query at every target. Send errors are counted but
	// do not abort the fan-out.
	asked := 0
	for _, t := range targets {
		if err := sender.Send(t.Addr, payload); err != nil {
			p.log.Debug("swarmsearch.query.send_fail",
				"peer", t.Addr, "txid", txid, "err", err)
			continue
		}
		asked++
	}

	if asked == 0 {
		return &QueryResponse{TxID: txid}, nil
	}

	// Collect responses with a merged deadline (caller ctx + timeout).
	queryCtx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	var (
		responses []incomingResult
		rejects   int
	)
collect:
	for len(responses)+rejects < asked {
		select {
		case <-queryCtx.Done():
			break collect
		case ir := <-pend.results:
			if ir.result.MsgType == MsgTypeReject {
				rejects++
				continue
			}
			responses = append(responses, ir)
		}
	}

	merged := mergeResponses(responses)
	return &QueryResponse{
		TxID:      txid,
		Hits:      merged,
		Asked:     asked,
		Responded: len(responses),
		Rejected:  rejects,
	}, nil
}

// routeResult is called by HandleMessage when an inbound Result
// message arrives. It looks up the matching pendingQuery and
// delivers the result to the collector. Results without a matching
// pending query (stale responses, spurious messages) are dropped.
func (p *Protocol) routeResult(peerAddr string, r Result) {
	pend := p.lookupPending(r.TxID)
	if pend == nil {
		p.log.Debug("swarmsearch.route_result.no_pending",
			"peer", peerAddr, "txid", r.TxID)
		return
	}
	select {
	case pend.results <- incomingResult{peer: peerAddr, result: r}:
	default:
		// Collector buffer full — drop the extra to avoid blocking
		// the caller (which runs from the read loop).
		p.log.Debug("swarmsearch.route_result.buffer_full",
			"peer", peerAddr, "txid", r.TxID)
	}
}

// routeReject is the same idea for Reject messages: look up the
// txid, deliver a reject-shaped Result so the collector sees the
// outcome. Stale rejects are dropped.
func (p *Protocol) routeReject(peerAddr string, r Reject) {
	pend := p.lookupPending(r.TxID)
	if pend == nil {
		return
	}
	select {
	case pend.results <- incomingResult{
		peer: peerAddr,
		result: Result{
			MsgType: MsgTypeReject,
			TxID:    r.TxID,
		},
	}:
	default:
	}
}

// mergeResponses deduplicates hits by infohash across the set of
// responses, summing ranks, taking the max seeder count, and
// preserving the first non-empty name/size. Returns the merged list
// sorted by score descending.
func mergeResponses(responses []incomingResult) []MergedHit {
	merged := make(map[string]*MergedHit)

	for _, ir := range responses {
		for _, h := range ir.result.Hits {
			ih := hex.EncodeToString(h.IH)
			if len(ih) != 40 {
				continue
			}
			m, ok := merged[ih]
			if !ok {
				m = &MergedHit{InfoHash: ih}
				merged[ih] = m
			}
			if m.Name == "" && h.N != "" {
				m.Name = h.N
			}
			if m.Size == 0 && h.Sz > 0 {
				m.Size = h.Sz
			}
			if h.S > m.Seeders {
				m.Seeders = h.S
			}
			m.Score += h.Rank
			if m.Score > 1000 {
				m.Score = 1000
			}
			m.Matches = append(m.Matches, h.Matches...)
			m.Sources = append(m.Sources, ir.peer)
		}
	}

	out := make([]MergedHit, 0, len(merged))
	for _, m := range merged {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Seeders > out[j].Seeders
	})
	return out
}

var _ = sync.Mutex{} // keep the sync import in play even if future refactors remove direct uses
