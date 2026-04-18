package swarmsearch

import (
	"context"
	"time"
)

// FeelerInterval is how often the background feeler goroutine
// probes one random peer from the PeerBook's "new" table.
// Every FeelerInterval, the goroutine picks one random new
// peer, sends a lightweight query to it, and promotes on
// success. Mirrors Bitcoin Core's feeler connection cadence
// (~2 minutes; we use 30s in production and 2s in regtest).
//
// The feeler query is invisible to the caller — it's a
// self-initiated background probe, not a user-facing search.
const (
	FeelerIntervalProd    = 30 * time.Second
	FeelerIntervalRegtest = 2 * time.Second
)

// feelerQuery is the query string the feeler sends. It's a
// syntactically valid but unlikely-to-match query so the
// remote peer answers quickly without hitting its index hard.
// The feeler doesn't care about the result content — it only
// cares whether the peer responds at all.
const feelerQuery = "__sn_feeler__"

// StartFeeler launches a background goroutine that periodically
// probes one random "new" peer to promote it to "tried". The
// goroutine runs until the provided context is cancelled.
//
// The feeler uses the Protocol's Sender + handleQuery path on
// the remote side, so it exercises the SAME code path that a
// real user query would. A peer that responds to a feeler gets
// promoted exactly like a peer that responds to a real query —
// because it IS a real sn_search exchange, just self-initiated.
//
// Call this once from engine startup after the Protocol is fully
// wired. Safe to call multiple times (subsequent calls are no-ops
// if a feeler is already running), but that's a misuse — the
// caller should track its own goroutine lifecycle.
func (p *Protocol) StartFeeler(ctx context.Context, interval time.Duration) {
	go p.feelerLoop(ctx, interval)
}

func (p *Protocol) feelerLoop(ctx context.Context, interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			p.feelerOnce(ctx)
		}
	}
}

func (p *Protocol) feelerOnce(ctx context.Context) {
	if p.book == nil {
		return
	}
	// Early-out if there's nothing untried to probe. Query()
	// itself would also skip the new-peer portion, but the
	// early return avoids constructing the query + context
	// when we know it can't promote anything.
	if len(p.book.NewAddrs()) == 0 {
		return
	}

	// Issue a lightweight query. Query's selectTargets picks up
	// to FeelerCount (2) new peers alongside every tried peer,
	// so firing *any* query is enough to give untried peers a
	// chance to respond and earn promotion. The feeler itself
	// doesn't target a specific address — it just triggers the
	// query that opens the promotion window.
	//
	// Timeout is short because the feeler shouldn't block on
	// slow peers.
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// The response is discarded on purpose: if any peer
	// replied, Query's collect path already called Promote on
	// it. We just care that the round-trip happened.
	_, _ = p.Query(queryCtx, QueryRequest{
		Q:       feelerQuery,
		Timeout: 3 * time.Second,
	})
}
