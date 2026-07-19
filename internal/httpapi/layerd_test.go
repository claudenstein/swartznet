package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
)

func startSearchServer(t *testing.T, opts Options) string {
	t.Helper()
	s := NewWithOptions("localhost:0", slog.New(slog.NewTextHandler(io.Discard, nil)), opts)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = s.Stop(ctx)
	})
	return s.Addr()
}

func postSearch(t *testing.T, addr string, body SearchRequestBody) *http.Response {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", "http://"+addr+"/search", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:7654")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// TestSearchLayerErrorAsymmetry pins §5.9: a Layer-L error is fatal (500),
// while a Layer-D error is surfaced inline (200 with dht.error), never a 5xx.
func TestSearchLayerErrorAsymmetry(t *testing.T) {
	t.Parallel()

	// Layer-L error → 500.
	addr := startSearchServer(t, Options{Search: func(SearchParams) SearchResult {
		return SearchResult{LocalErr: errors.New("bleve exploded")}
	}})
	resp := postSearch(t, addr, SearchRequestBody{Q: "ubuntu"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("Layer-L error status = %d, want 500", resp.StatusCode)
	}

	// Layer-D error → 200 with dht.error.
	addr2 := startSearchServer(t, Options{Search: func(p SearchParams) SearchResult {
		out := SearchResult{Local: LocalBlock{Hits: []LocalHit{}}}
		if p.DHT {
			out.Dht = &DHTBlock{Hits: []DHTHit{}, Error: "dht unreachable"}
		}
		return out
	}})
	resp2 := postSearch(t, addr2, SearchRequestBody{Q: "ubuntu", DHT: true})
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("Layer-D error status = %d, want 200", resp2.StatusCode)
	}
	var doc SearchResponse
	if err := json.NewDecoder(resp2.Body).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	if doc.Dht == nil || doc.Dht.Error != "dht unreachable" {
		t.Fatalf("dht block = %+v, want inline error", doc.Dht)
	}
}

// TestSearchDHTBlockRendered confirms a successful Layer-D response renders its
// block with hits, and that the block is omitted when the request didn't ask.
func TestSearchDHTBlockRendered(t *testing.T) {
	t.Parallel()
	search := func(p SearchParams) SearchResult {
		out := SearchResult{Local: LocalBlock{Hits: []LocalHit{}}}
		if p.DHT {
			out.Dht = &DHTBlock{
				IndexersAsked:     2,
				IndexersResponded: 1,
				Hits:              []DHTHit{{InfoHash: strings.Repeat("a", 40), Name: "ubuntu", Score: 0.7, Sources: []string{"idx"}}},
			}
		}
		return out
	}
	addr := startSearchServer(t, Options{Search: search})

	// Without --dht: no dht block.
	r1 := postSearch(t, addr, SearchRequestBody{Q: "ubuntu"})
	defer r1.Body.Close()
	var d1 SearchResponse
	json.NewDecoder(r1.Body).Decode(&d1)
	if d1.Dht != nil {
		t.Errorf("dht block present without the flag: %+v", d1.Dht)
	}

	// With --dht: block rendered.
	r2 := postSearch(t, addr, SearchRequestBody{Q: "ubuntu", DHT: true})
	defer r2.Body.Close()
	var d2 SearchResponse
	json.NewDecoder(r2.Body).Decode(&d2)
	if d2.Dht == nil || d2.Dht.IndexersAsked != 2 || len(d2.Dht.Hits) != 1 {
		t.Fatalf("dht block = %+v", d2.Dht)
	}
}

// TestPublishRoute renders the Layer-D publisher state, including the pubkey
// (independent of a publisher collaborator) and the per-keyword list.
func TestPublishRoute(t *testing.T) {
	t.Parallel()
	pk := strings.Repeat("cd", 32)
	addr := startSearchServer(t, Options{
		PublisherPubKey: func() string { return pk },
		PublisherStatus: func() PublisherStatus {
			return PublisherStatus{
				TotalKeywords: 1,
				TotalHits:     3,
				Keywords: []PublisherKeywordEntry{
					{Keyword: "ubuntu", HitsCount: 3, PublishCount: 2, LastPublished: "2026-07-19T00:00:00Z"},
				},
			}
		},
	})
	resp, err := http.Get("http://" + addr + "/publish")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var doc PublisherStatus
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	if doc.PubKey != pk || doc.TotalKeywords != 1 || doc.TotalHits != 3 || len(doc.Keywords) != 1 {
		t.Fatalf("publish doc = %+v", doc)
	}
	if doc.Keywords[0].Keyword != "ubuntu" || doc.Keywords[0].PublishCount != 2 {
		t.Errorf("keyword entry = %+v", doc.Keywords[0])
	}
}

// TestPublishRouteEmptyPublisher: with only an identity (no publisher wired),
// the pubkey still renders and totals are zero.
func TestPublishRouteEmptyPublisher(t *testing.T) {
	t.Parallel()
	pk := strings.Repeat("ef", 32)
	addr := startSearchServer(t, Options{PublisherPubKey: func() string { return pk }})
	resp, err := http.Get("http://" + addr + "/publish")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var doc PublisherStatus
	json.NewDecoder(resp.Body).Decode(&doc)
	if doc.PubKey != pk || doc.TotalKeywords != 0 {
		t.Fatalf("publish doc = %+v", doc)
	}
}
