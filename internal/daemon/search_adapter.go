package daemon

import (
	"context"

	"github.com/swartznet/swartznet/internal/httpapi"
	"github.com/swartznet/swartznet/internal/indexer"
	"github.com/swartznet/swartznet/internal/searchmux"
)

// search returns the httpapi Search collaborator, translating between
// httpapi's local DTOs and the searchmux/indexer types field by field so
// httpapi imports neither.
func (a *controllerAdapter) search(mux *searchmux.Mux) func(httpapi.SearchParams) httpapi.SearchResult {
	return func(p httpapi.SearchParams) httpapi.SearchResult {
		res := mux.Search(context.Background(), searchmux.Query{
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
		return httpapi.SearchResult{Local: localBlock(res.Local)}
	}
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
