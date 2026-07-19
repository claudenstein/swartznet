// Package searchmux fans a query out across the three search layers and
// returns their native response types side by side — never a merged hit
// type. Slice 4 shipped Layer L; Slice 7 adds Layer S (swarm). The DHT slot
// stays nil until its slice lands. Sharing this one fan-out between the HTTP
// adapter and the GUI is what keeps their reconciliation from drifting.
package searchmux

import (
	"context"
	"sync"

	"github.com/swartznet/swartznet/internal/indexer"
	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// LocalSearcher is Layer L (the Bleve index). Declared here and satisfied by
// a daemon adapter, so searchmux imports no engine or HTTP code.
type LocalSearcher interface {
	Search(req indexer.SearchRequest) (*indexer.SearchResponse, error)
}

// SwarmSearcher is Layer S (sn_search peer-wire). Satisfied by a daemon
// adapter over the engine's swarmsearch.Protocol.
type SwarmSearcher interface {
	SwarmSearch(ctx context.Context, req swarmsearch.QueryRequest) (*swarmsearch.QueryResponse, error)
}

// Query is one search across the layers. Swarm/DHT toggles are carried now;
// DHT is ignored until its layer exists.
type Query struct {
	Text      string
	Limit     int
	SignedBy  string
	Highlight bool
	Swarm     bool
	Scope     string
	DHT       bool
}

// Result carries each layer's native response plus its error. Layer-L error is
// fatal to the whole request (the caller maps it to 500); the swarm error is
// surfaced inline (a 200 with a swarm.error string — §5.9).
type Result struct {
	Local    *indexer.SearchResponse
	LocalErr error
	Swarm    *swarmsearch.QueryResponse
	SwarmErr error
	// DHT   *dhtindex.LookupResponse  — Slice 9
}

// Mux fans out. A nil local searcher yields an empty local response (Layer L
// simply off), never an error — the "always run Layer L when wired" contract.
type Mux struct {
	Local LocalSearcher
	Swarm SwarmSearcher
}

// Search runs the query. Layer L and (when requested + wired) Layer S run
// concurrently; the caller passes a ctx whose deadline bounds the swarm.
func (m *Mux) Search(ctx context.Context, q Query) Result {
	var res Result
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		if m.Local == nil {
			res.Local = &indexer.SearchResponse{Hits: []indexer.SearchHit{}}
			return
		}
		resp, err := m.Local.Search(indexer.SearchRequest{
			Query:     q.Text,
			Limit:     q.Limit,
			Highlight: q.Highlight,
			SignedBy:  q.SignedBy,
		})
		if err != nil {
			res.LocalErr = err
			return
		}
		res.Local = resp
	}()

	if q.Swarm && m.Swarm != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := m.Swarm.SwarmSearch(ctx, swarmsearch.QueryRequest{
				Q: q.Text, Scope: q.Scope, Limit: q.Limit,
			})
			if err != nil {
				res.SwarmErr = err
				return
			}
			res.Swarm = resp
		}()
	}

	wg.Wait()
	return res
}
