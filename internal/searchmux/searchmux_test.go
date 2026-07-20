package searchmux

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/dhtindex"
	"github.com/swartznet/swartznet/internal/indexer"
	"github.com/swartznet/swartznet/internal/swarmsearch"
)

type fakeLocal struct {
	resp *indexer.SearchResponse
	err  error
	last indexer.SearchRequest
}

func (f *fakeLocal) Search(req indexer.SearchRequest) (*indexer.SearchResponse, error) {
	f.last = req
	return f.resp, f.err
}

func TestNilLocalYieldsEmptyBlock(t *testing.T) {
	m := &Mux{}
	res := m.Search(context.Background(), Query{Text: "x"})
	if res.LocalErr != nil || res.Local == nil || len(res.Local.Hits) != 0 {
		t.Fatalf("nil local: %+v", res)
	}
}

func TestLocalErrorPropagates(t *testing.T) {
	m := &Mux{Local: &fakeLocal{err: errors.New("boom")}}
	res := m.Search(context.Background(), Query{Text: "x"})
	if res.LocalErr == nil {
		t.Fatal("want LocalErr")
	}
}

func TestQueryFieldsThreaded(t *testing.T) {
	fl := &fakeLocal{resp: &indexer.SearchResponse{}}
	m := &Mux{Local: fl}
	m.Search(context.Background(), Query{Text: "ubuntu", Limit: 7, SignedBy: "abc", Highlight: true})
	if fl.last.Query != "ubuntu" || fl.last.Limit != 7 || fl.last.SignedBy != "abc" || !fl.last.Highlight {
		t.Fatalf("request = %+v", fl.last)
	}
}

// --- Layer D (Slice 9) ---

type fakeDHT struct {
	resp  *dhtindex.LookupResponse
	err   error
	delay time.Duration
	calls int32
}

func (f *fakeDHT) DHTSearch(ctx context.Context, q string, limit int) (*dhtindex.LookupResponse, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.resp, f.err
}

func TestDHTBranchGatedOnFlagAndWiring(t *testing.T) {
	fd := &fakeDHT{resp: &dhtindex.LookupResponse{IndexersAsked: 3}}
	m := &Mux{Local: &fakeLocal{resp: &indexer.SearchResponse{}}, DHT: fd}
	// DHT=false → the branch never runs.
	m.Search(context.Background(), Query{Text: "x", DHT: false})
	if atomic.LoadInt32(&fd.calls) != 0 {
		t.Fatal("DHT branch ran without the flag")
	}
	// DHT=true → it runs and its native response is carried.
	res := m.Search(context.Background(), Query{Text: "x", DHT: true})
	if atomic.LoadInt32(&fd.calls) != 1 || res.DHT == nil || res.DHT.IndexersAsked != 3 {
		t.Fatalf("DHT branch did not carry the response: %+v", res.DHT)
	}
}

func TestDHTErrorSurfacedInline(t *testing.T) {
	m := &Mux{Local: &fakeLocal{resp: &indexer.SearchResponse{}}, DHT: &fakeDHT{err: errors.New("dht boom")}}
	res := m.Search(context.Background(), Query{Text: "x", DHT: true})
	// A Layer-D error must NOT become a fatal LocalErr — it rides res.DHTErr.
	if res.LocalErr != nil {
		t.Fatal("Layer-D error leaked into LocalErr")
	}
	if res.DHTErr == nil {
		t.Fatal("want DHTErr")
	}
}

// TestSearchmuxConcurrentLatency proves the three layers run concurrently:
// each collaborator sleeps 300ms, so a sequential fan-out would take ~900ms
// while the concurrent one finishes in ~max, well under 600ms.
func TestSearchmuxConcurrentLatency(t *testing.T) {
	const d = 300 * time.Millisecond
	m := &Mux{
		Local: &fakeLocal{resp: &indexer.SearchResponse{}},
		Swarm: &slowSwarm{delay: d},
		DHT:   &fakeDHT{resp: &dhtindex.LookupResponse{}, delay: d},
	}
	start := time.Now()
	m.Search(context.Background(), Query{Text: "x", Swarm: true, DHT: true})
	if elapsed := time.Since(start); elapsed > 600*time.Millisecond {
		t.Fatalf("layers did not run concurrently: elapsed %v (want ~%v, not the ~%v sum)", elapsed, d, 3*d)
	}
}

type slowSwarm struct{ delay time.Duration }

func (s *slowSwarm) SwarmSearch(ctx context.Context, req swarmsearch.QueryRequest) (*swarmsearch.QueryResponse, error) {
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &swarmsearch.QueryResponse{}, nil
}
