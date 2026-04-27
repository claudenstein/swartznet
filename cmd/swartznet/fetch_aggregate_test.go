package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestFetchAggregateBlockReturnsNilOnNon200 covers the
// `if resp.StatusCode != http.StatusOK { return nil }` arm.
func TestFetchAggregateBlockReturnsNilOnNon200(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	got := fetchAggregateBlock(context.Background(), addr)
	if got != nil {
		t.Errorf("got %+v, want nil for 500 response", got)
	}
}

// TestFetchAggregateBlockReturnsNilOnDecodeError covers the
// `if err := json.NewDecoder(...).Decode(&a); err != nil` arm.
func TestFetchAggregateBlockReturnsNilOnDecodeError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "this is not JSON {")
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	got := fetchAggregateBlock(context.Background(), addr)
	if got != nil {
		t.Errorf("got %+v, want nil for non-JSON body", got)
	}
}

// TestFetchAggregateBlockReturnsNilOnDoError covers the
// `resp, err := http.DefaultClient.Do(req); if err != nil { return nil }`
// arm — point at a port that nothing is listening on.
func TestFetchAggregateBlockReturnsNilOnDoError(t *testing.T) {
	t.Parallel()
	// 127.0.0.1:1 reserved + nothing listens there.
	got := fetchAggregateBlock(context.Background(), "127.0.0.1:1")
	if got != nil {
		t.Errorf("got %+v, want nil for unreachable addr", got)
	}
}

// TestFetchAggregateBlockHappyPath covers the success path —
// 200 OK with a valid JSON body returns the parsed response.
func TestFetchAggregateBlockHappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ppmi_enabled":true,"known_indexers":7}`)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	got := fetchAggregateBlock(context.Background(), addr)
	if got == nil {
		t.Fatal("got nil, want parsed response")
	}
	if !got.PPMIEnabled {
		t.Errorf("PPMIEnabled = false, want true")
	}
	if got.KnownIndexers != 7 {
		t.Errorf("KnownIndexers = %d, want 7", got.KnownIndexers)
	}
}
