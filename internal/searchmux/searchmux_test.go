package searchmux

import (
	"context"
	"errors"
	"testing"

	"github.com/swartznet/swartznet/internal/indexer"
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
