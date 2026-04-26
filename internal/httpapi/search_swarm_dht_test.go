package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"

	pp "github.com/anacrolix/torrent/peer_protocol"
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

// scriptedSender forwards every Query to a callback that
// synthesises a Result via HandleMessage. Lets the swarm
// happy-path hit-iteration fire in HTTP-level tests.
type scriptedSender struct {
	p    *swarmsearch.Protocol
	hits []swarmsearch.Hit
}

func (s *scriptedSender) Send(peer string, payload []byte) error {
	q, err := swarmsearch.DecodeQuery(payload)
	if err != nil {
		return err
	}
	go func() {
		resPayload, _ := swarmsearch.EncodeResult(swarmsearch.Result{
			TxID:  q.TxID,
			Total: len(s.hits),
			Hits:  s.hits,
		})
		s.p.HandleMessage(peer, resPayload, nil)
	}()
	return nil
}

// TestHTTPSearchSwarmReturnsHits covers handleSearch's swarm
// success-with-hits iteration arm. Mark a peer capable, plug
// in a scriptedSender that synthesises a Result with one
// hit, and verify the HTTP response carries the hit
// translated into a SwarmHit.
func TestHTTPSearchSwarmReturnsHits(t *testing.T) {
	t.Parallel()
	sw := swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	scripted := &scriptedSender{
		p: sw,
		hits: []swarmsearch.Hit{
			{IH: bytes.Repeat([]byte{0xab}, 20), N: "Ubuntu Swarm", S: 100, Sz: 6 << 30},
		},
	}
	sw.SetSender(scripted)
	const peer = "1.2.3.4:6881"
	sw.NotePeerAdded(peer)
	sw.OnRemoteHandshake(peer, &pp.ExtendedHandshakeMessage{
		M: map[pp.ExtensionName]pp.ExtensionNumber{
			swarmsearch.ExtensionName: 11,
		},
	})

	idx := openTempIndex(t)
	base := startServer(t, httpapi.Options{Index: idx, Swarm: sw})

	body, _ := json.Marshal(httpapi.SearchRequest{
		Q:              "ubuntu",
		Swarm:          true,
		SwarmTimeoutMs: 200,
	})
	resp, err := http.Post(base+"/search", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got httpapi.SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Swarm == nil || len(got.Swarm.Hits) == 0 {
		t.Fatalf("expected swarm hits, got %+v", got.Swarm)
	}
	if got.Swarm.Hits[0].Name != "Ubuntu Swarm" {
		t.Errorf("Name = %q, want 'Ubuntu Swarm'", got.Swarm.Hits[0].Name)
	}
}

// fixedHitGetter returns the same KeywordValue for every
// (pubkey, salt) pair. Used to drive Lookup.Query into its
// happy path so handleSearch's hit-iteration loop fires.
type fixedHitGetter struct {
	hits []dhtindex.KeywordHit
}

func (g fixedHitGetter) Get(_ context.Context, _ [32]byte, _ []byte) (dhtindex.KeywordValue, error) {
	return dhtindex.KeywordValue{Hits: g.hits}, nil
}

// TestHTTPSearchDHTReturnsHits covers handleSearch's
// `for _, h := range out.Hits { dhtResp.Hits = append(...) }`
// arm. Wire a Lookup with a fixedHitGetter that returns one
// hit, register an indexer, then post a query and verify the
// HTTP response carries the hit translated into a DHTHit.
func TestHTTPSearchDHTReturnsHits(t *testing.T) {
	t.Parallel()
	getter := fixedHitGetter{hits: []dhtindex.KeywordHit{
		{IH: bytes.Repeat([]byte{0xaa}, 20), N: "Ubuntu DHT", S: 100, F: 4, Sz: 6 << 30},
	}}
	lookup := dhtindex.NewLookup(getter)
	var pub [32]byte
	pub[0] = 0xab
	lookup.AddIndexer(pub, "indexer-one")
	idx := openTempIndex(t)
	base := startServer(t, httpapi.Options{Index: idx, Lookup: lookup})

	body, _ := json.Marshal(httpapi.SearchRequest{
		Q:            "ubuntu",
		DHT:          true,
		DHTTimeoutMs: 200,
	})
	resp, err := http.Post(base+"/search", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got httpapi.SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.DHT == nil || len(got.DHT.Hits) == 0 {
		t.Fatalf("expected DHT hits, got %+v", got.DHT)
	}
	if got.DHT.Hits[0].Name != "Ubuntu DHT" {
		t.Errorf("hit Name = %q, want 'Ubuntu DHT'", got.DHT.Hits[0].Name)
	}
}

// TestHTTPSearchDHTQueryError covers handleSearch's
// `if err != nil { dhtResp.Error = err.Error() }` arm in the
// DHT path. dhtindex.Lookup.Query rejects queries that
// produce no tokens — passing an all-stopword query
// ("the of") makes Tokenize return empty and Lookup.Query
// surfaces the error. The HTTP layer must still respond 200
// and put the error string in the dht.error field.
func TestHTTPSearchDHTQueryError(t *testing.T) {
	t.Parallel()
	// Need at least one indexer registered for Lookup.Query
	// to even attempt tokenisation.
	lookup := dhtindex.NewLookup(emptyDHTGetter{})
	idx := openTempIndex(t)
	base := startServer(t, httpapi.Options{Index: idx, Lookup: lookup})

	body, _ := json.Marshal(httpapi.SearchRequest{
		Q:            "the of", // all-stopword → Tokenize returns empty
		DHT:          true,
		DHTTimeoutMs: 50,
	})
	resp, err := http.Post(base+"/search", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d (expected 200 even on DHT-side error)", resp.StatusCode)
	}
	var got httpapi.SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.DHT == nil {
		t.Fatal("DHT result should not be nil when Lookup is configured")
	}
	if got.DHT.Error == "" {
		t.Errorf("expected DHT.Error to be populated, got %q", got.DHT.Error)
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
