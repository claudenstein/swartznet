package daemon

import (
	"context"
	"time"

	"github.com/swartznet/swartznet/internal/engine"
	"github.com/swartznet/swartznet/internal/httpapi"
	"github.com/swartznet/swartznet/internal/indexer"
	"github.com/swartznet/swartznet/internal/searchmux"
	"github.com/swartznet/swartznet/internal/swarmsearch"
)

const (
	maxSearchTimeout     = 30 * time.Second
	defaultSwarmTimeout  = 2 * time.Second
	swarmCtxTimeoutGrace = 500 * time.Millisecond
)

// swarmSearchAdapter satisfies searchmux.SwarmSearcher over the engine's
// sn_search protocol.
type swarmSearchAdapter struct{ eng *engine.Engine }

func (s *swarmSearchAdapter) SwarmSearch(ctx context.Context, req swarmsearch.QueryRequest) (*swarmsearch.QueryResponse, error) {
	return s.eng.SwarmSearch().Query(ctx, req)
}

// clampSearchTimeout bounds a requested swarm timeout to (0, maxSearchTimeout],
// falling back to the default when unset.
func clampSearchTimeout(ms int, def time.Duration) time.Duration {
	if ms <= 0 {
		return def
	}
	d := time.Duration(ms) * time.Millisecond
	if d > maxSearchTimeout {
		return maxSearchTimeout
	}
	return d
}

// search returns the httpapi Search collaborator, translating between
// httpapi's DTOs and the searchmux/indexer/swarmsearch types field by field so
// httpapi imports none of them. A Layer-S failure is returned inline (a
// SwarmBlock with an Error), never as a fatal error (§5.9).
func (a *controllerAdapter) search(mux *searchmux.Mux) func(httpapi.SearchParams) httpapi.SearchResult {
	return func(p httpapi.SearchParams) httpapi.SearchResult {
		ctx := context.Background()
		var cancel context.CancelFunc
		if p.Swarm {
			timeout := clampSearchTimeout(p.SwarmTimeoutMS, defaultSwarmTimeout)
			ctx, cancel = context.WithTimeout(ctx, timeout+swarmCtxTimeoutGrace)
			defer cancel()
		}
		res := mux.Search(ctx, searchmux.Query{
			Text:      p.Query,
			Limit:     p.Limit,
			SignedBy:  p.SignedBy,
			Highlight: p.Highlight,
			Swarm:     p.Swarm,
			DHT:       p.DHT,
		})
		if res.LocalErr != nil {
			return httpapi.SearchResult{LocalErr: res.LocalErr}
		}
		out := httpapi.SearchResult{Local: localBlock(res.Local)}
		if p.Swarm {
			out.Swarm = swarmBlock(res.Swarm, res.SwarmErr)
		}
		return out
	}
}

// swarmBlock translates a Layer-S response (or error) to the httpapi DTO. An
// error renders as a block carrying only the Error string (200, §5.9).
func swarmBlock(resp *swarmsearch.QueryResponse, err error) *httpapi.SwarmBlock {
	block := &httpapi.SwarmBlock{Hits: []httpapi.SwarmHit{}}
	if err != nil {
		block.Error = err.Error()
		return block
	}
	if resp == nil {
		return block
	}
	block.Asked = resp.Asked
	block.Responded = resp.Responded
	block.Rejected = resp.Rejected
	for _, h := range resp.Hits {
		block.Hits = append(block.Hits, httpapi.SwarmHit{
			InfoHash: h.InfoHash,
			Name:     h.Name,
			Size:     h.Size,
			Seeders:  h.Seeders,
			Score:    h.Score,
			Sources:  h.Sources,
		})
	}
	return block
}

func localBlock(resp *indexer.SearchResponse) httpapi.LocalBlock {
	block := httpapi.LocalBlock{Hits: []httpapi.LocalHit{}}
	if resp == nil {
		return block
	}
	block.Total = resp.Total
	for _, h := range resp.Hits {
		block.Hits = append(block.Hits, httpapi.LocalHit{
			DocType:   h.DocType,
			InfoHash:  h.InfoHash,
			Name:      h.Name,
			SizeBytes: h.SizeBytes,
			FileIndex: h.FileIndex,
			FilePath:  h.FilePath,
			Mime:      h.Mime,
			Extractor: h.Extractor,
			Score:     h.Score,
			SignedBy:  h.SignedBy,
			Fragments: h.Fragments,
		})
	}
	return block
}

func (a *controllerAdapter) indexStats() (httpapi.IndexStats, error) {
	idx := a.eng.Index()
	if idx == nil {
		return httpapi.IndexStats{}, nil
	}
	s, err := idx.Stats()
	if err != nil {
		return httpapi.IndexStats{}, err
	}
	return httpapi.IndexStats{
		DirBytes:        s.DirBytes,
		DocCount:        s.DocCount,
		TorrentCount:    s.TorrentCount,
		ContentCount:    s.ContentCount,
		CorpusTextBytes: s.CorpusTextBytes,
		InflationRatio:  s.InflationRatio,
	}, nil
}

func (a *controllerAdapter) localDocCount() (uint64, error) {
	idx := a.eng.Index()
	if idx == nil {
		return 0, nil
	}
	return idx.DocCount()
}

// bloomStat reports the known-good Bloom for /status (nil = block omitted).
func (a *controllerAdapter) bloomStat() *httpapi.BloomStatus {
	bloom := a.eng.KnownGoodBloom()
	if bloom == nil {
		return nil
	}
	return &httpapi.BloomStatus{
		BitSize:        bloom.Bits(),
		HashFunctions:  bloom.HashFunctions(),
		PopulationBits: bloom.PopulationCount(),
		EstimatedItems: bloom.EstimatedItems(),
	}
}

// reputationStat reports the reputation tracker for /status (top 10 by score).
func (a *controllerAdapter) reputationStat() *httpapi.ReputationStat {
	tracker := a.eng.ReputationTracker()
	if tracker == nil {
		return nil
	}
	snap := tracker.Snapshot() // sorted by score, tie-broken deterministically
	out := &httpapi.ReputationStat{KnownIndexers: len(snap)}
	for i, e := range snap {
		if i >= 10 {
			break
		}
		out.TopIndexers = append(out.TopIndexers, httpapi.ReputationIndexerSummary{
			PubKey:        string(e.PubKey),
			Score:         e.Score,
			HitsReturned:  e.Counters.HitsReturned,
			HitsConfirmed: e.Counters.HitsConfirmed,
			HitsFlagged:   e.Counters.HitsFlagged,
		})
	}
	return out
}

// aggregate reports the admission counts. The Services field is filled by the
// httpapi server from ServicesReporter (the single live-mask render path), so
// it is deliberately left unset here. Distinguishes a starved node from a
// quiet one.
func (a *controllerAdapter) aggregate() httpapi.AggregateStatusResponse {
	resp := httpapi.AggregateStatusResponse{}
	if t := a.eng.ReputationTracker(); t != nil {
		resp.KnownIndexers = len(t.Snapshot())
	}
	if a.adm != nil {
		resp.Bootstrap = httpapi.AggregateBootstrap{
			Anchors:  a.adm.AnchorCount(),
			Admitted: a.adm.AdmittedCount(),
			Pending:  a.adm.PendingCount(),
		}
	}
	resp.CacheSize = a.eng.RecordCache().Len()
	resp.Reconciliation = a.eng.RuntimeFacts().Reconciliation
	return resp
}
