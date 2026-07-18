package indexer

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/search"
)

// fullPageBleve is a pathological bleve.Index stub whose Search always
// returns a completely full page, no matter how deep the caller
// paginates — the "never returns a short page" failure mode the
// AllTorrentDocs / ContentDocsForInfoHash maxPages backstop defends
// against. A count-only request (Size == 0) reports `total`, so the
// walk's ceiling is scaled off that. Only Search is implemented; the
// embedded nil interface panics on anything else, which doubles as an
// assertion that the walk touches nothing besides Search.
type embeddedBleveIndex = bleve.Index // alias so the embedded field name does not collide with the Index method

type fullPageBleve struct {
	embeddedBleveIndex
	total uint64
}

func (f *fullPageBleve) Search(req *bleve.SearchRequest) (*bleve.SearchResult, error) {
	if req.Size == 0 {
		return &bleve.SearchResult{Total: f.total}, nil
	}
	hits := make(search.DocumentMatchCollection, req.Size)
	for n := range hits {
		hits[n] = &search.DocumentMatch{
			Fields: map[string]any{
				fieldInfoHash: fmt.Sprintf("%040d", req.From+n),
			},
		}
	}
	return &bleve.SearchResult{Total: f.total, Hits: hits}, nil
}

// warnCounter counts slog records whose message has the given prefix.
type warnCounter struct {
	prefix string
	count  *atomic.Int64
}

func (h warnCounter) Enabled(context.Context, slog.Level) bool { return true }
func (h warnCounter) Handle(_ context.Context, r slog.Record) error {
	if strings.HasPrefix(r.Message, h.prefix) {
		h.count.Add(1)
	}
	return nil
}
func (h warnCounter) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h warnCounter) WithGroup(string) slog.Handler      { return h }

// TestAllTorrentDocsBoundedAgainstFullPages is the regression for the
// unbounded deep-pagination walk: AllTorrentDocs holds the global
// mutex while paging, so against an index that never returns a short
// page it must stop at the scaled maxPages ceiling (and log the
// truncation) rather than loop forever. Before the fix this test
// never returned.
func TestAllTorrentDocsBoundedAgainstFullPages(t *testing.T) {
	t.Parallel()
	var seen atomic.Int64
	i := &Index{
		path:  "fake",
		bleve: &fullPageBleve{total: 2500},
		log:   slog.New(warnCounter{prefix: "indexer.all_torrent_docs_truncated", count: &seen}),
	}

	docs, err := i.AllTorrentDocs()
	if err != nil {
		t.Fatalf("AllTorrentDocs: %v", err)
	}
	// maxPages = 2500/1000 + 2 = 4 pages of 1000 docs each.
	if len(docs) != 4000 {
		t.Errorf("got %d docs, want 4000 (the maxPages ceiling)", len(docs))
	}
	if got := seen.Load(); got != 1 {
		t.Errorf("expected exactly one truncation warning, got %d", got)
	}
}

// TestContentDocsForInfoHashBoundedAgainstFullPages is the same
// regression for the content-doc walk.
func TestContentDocsForInfoHashBoundedAgainstFullPages(t *testing.T) {
	t.Parallel()
	var seen atomic.Int64
	i := &Index{
		path:  "fake",
		bleve: &fullPageBleve{total: 1500},
		log:   slog.New(warnCounter{prefix: "indexer.content_docs_truncated", count: &seen}),
	}

	docs, err := i.ContentDocsForInfoHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("ContentDocsForInfoHash: %v", err)
	}
	// maxPages = 1500/1000 + 2 = 3 pages of 1000 docs each.
	if len(docs) != 3000 {
		t.Errorf("got %d docs, want 3000 (the maxPages ceiling)", len(docs))
	}
	if got := seen.Load(); got != 1 {
		t.Errorf("expected exactly one truncation warning, got %d", got)
	}
}
