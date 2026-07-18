// Package searchmux fans a query out across the three search layers and
// returns their native response types side by side — never a merged hit
// type. Slice 4 ships Layer L only; the swarm and DHT slots stay nil until
// their slices land. Sharing this one fan-out between the HTTP adapter and
// the GUI is what keeps their reconciliation from drifting.
package searchmux

import (
	"context"

	"github.com/swartznet/swartznet/internal/indexer"
)

// LocalSearcher is Layer L (the Bleve index). Declared here and satisfied by
// a daemon adapter, so searchmux imports no engine or HTTP code.
type LocalSearcher interface {
	Search(req indexer.SearchRequest) (*indexer.SearchResponse, error)
}

// Query is one search across the layers. Swarm/DHT fields are decoded and
// carried now but ignored until their layers exist.
type Query struct {
	Text      string
	Limit     int
	SignedBy  string
	Highlight bool
	Swarm     bool
	DHT       bool
}

// Result carries each layer's native response plus its error. Layer-L error
// is fatal to the whole request (the caller maps it to 500); swarm/DHT
// errors are surfaced inline (later slices).
type Result struct {
	Local    *indexer.SearchResponse
	LocalErr error
	// Swarm *swarmsearch.QueryResponse — Slice 7
	// DHT   *dhtindex.LookupResponse  — Slice 9
}

// Mux fans out. Only the local searcher exists in Slice 4; a nil local
// searcher yields an empty local response (Layer L simply off), never an
// error — the "always run Layer L when wired" contract.
type Mux struct {
	Local LocalSearcher
}

// Search runs the query. Layer L runs synchronously for now; the errgroup
// fan-out arrives with the concurrent layers.
func (m *Mux) Search(_ context.Context, q Query) Result {
	var res Result
	if m.Local == nil {
		res.Local = &indexer.SearchResponse{Hits: []indexer.SearchHit{}}
		return res
	}
	resp, err := m.Local.Search(indexer.SearchRequest{
		Query:     q.Text,
		Limit:     q.Limit,
		Highlight: q.Highlight,
		SignedBy:  q.SignedBy,
	})
	if err != nil {
		res.LocalErr = err
		return res
	}
	res.Local = resp
	return res
}
