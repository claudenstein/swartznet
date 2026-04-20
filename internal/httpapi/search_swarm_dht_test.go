package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/swartznet/swartznet/internal/dhtindex"
	"github.com/swartznet/swartznet/internal/httpapi"
	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// emptyDHTGetter returns "no value" for every (pubkey, salt) pair.
// dhtindex.Lookup.Query treats this as a per-indexer miss so the
// merged response is empty without raising an error.
type emptyDHTGetter struct{}

func (emptyDHTGetter) Get(ctx context.Context, pubkey [32]byte, salt []byte) (dhtindex.KeywordValue, error) {
	return dhtindex.KeywordValue{}, context.DeadlineExceeded
}

// TestHTTPSearchWithSwarmConfigured covers the Swarm-protocol
// branch in handleSearch. We wire a real but empty
// swarmsearch.Protocol; with zero capable peers Query returns an
// empty hits list (asked/responded both zero) — the response must
// carry an Asked field and an empty Hits slice.
func TestHTTPSearchWithSwarmConfigured(t *testing.T) {
	t.Parallel()
	swarm := swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	idx := openTempIndex(t)
	base := startServer(t, httpapi.Options{Index: idx, Swarm: swarm})

	body, _ := json.Marshal(httpapi.SearchRequest{
		Q:              "ubuntu",
		Swarm:          true,
		SwarmTimeoutMs: 50,
	})
	resp, err := http.Post(base+"/search", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, raw)
	}
	var got httpapi.SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Swarm == nil {
		t.Fatal("Swarm result should not be nil when protocol is configured")
	}
	if got.Swarm.Asked != 0 {
		t.Errorf("Asked = %d, want 0 (no peers)", got.Swarm.Asked)
	}
	if len(got.Swarm.Hits) != 0 {
		t.Errorf("Hits = %d, want 0", len(got.Swarm.Hits))
	}
}

// TestHTTPSearchWithDHTConfigured covers the Lookup branch in
// handleSearch. A Lookup wrapped around an emptyDHTGetter with no
// indexers registered returns IndexersAsked=0 and an empty hits
// list; the response must still include a non-nil DHT section.
func TestHTTPSearchWithDHTConfigured(t *testing.T) {
	t.Parallel()
	lookup := dhtindex.NewLookup(emptyDHTGetter{})
	idx := openTempIndex(t)
	base := startServer(t, httpapi.Options{Index: idx, Lookup: lookup})

	body, _ := json.Marshal(httpapi.SearchRequest{
		Q:            "ubuntu",
		DHT:          true,
		DHTTimeoutMs: 50,
	})
	resp, err := http.Post(base+"/search", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, raw)
	}
	var got httpapi.SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.DHT == nil {
		t.Fatal("DHT result should not be nil when lookup is configured")
	}
	if got.DHT.IndexersAsked != 0 {
		t.Errorf("IndexersAsked = %d, want 0 (no indexers registered)", got.DHT.IndexersAsked)
	}
	if len(got.DHT.Hits) != 0 {
		t.Errorf("Hits = %d, want 0", len(got.DHT.Hits))
	}
}

// TestHTTPSearchLimitCappedAtMax pins the documented limit cap.
// Requesting Limit far above maxSearchLimit must succeed but the
// handler silently caps the value (we can't observe the post-cap
// value directly, only that the request completes 200 with a
// well-formed response).
func TestHTTPSearchLimitCappedAtMax(t *testing.T) {
	t.Parallel()
	idx := openTempIndex(t)
	base := startServer(t, httpapi.Options{Index: idx})

	body, _ := json.Marshal(httpapi.SearchRequest{Q: "ubuntu", Limit: 999_999})
	resp, err := http.Post(base+"/search", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (limit should be silently capped)", resp.StatusCode)
	}
}

// TestHTTPSearchMissingQuery covers the documented "missing query
// field 'q'" 400.
func TestHTTPSearchMissingQuery(t *testing.T) {
	t.Parallel()
	base := startServer(t, httpapi.Options{Index: openTempIndex(t)})

	body, _ := json.Marshal(httpapi.SearchRequest{Q: ""})
	resp, err := http.Post(base+"/search", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}
