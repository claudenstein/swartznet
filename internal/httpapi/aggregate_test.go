package httpapi

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/dhtindex"
	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// startAggregateServer spins up an httpapi server with the
// supplied swarm + lookup, listening on localhost:0. Returns the
// base URL and a cleanup function.
func startAggregateServer(t *testing.T, swarm *swarmsearch.Protocol, lookup *dhtindex.Lookup) (base string, stop func()) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewWithOptions("127.0.0.1:0", log, Options{
		Swarm:  swarm,
		Lookup: lookup,
	})
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	addr := srv.Addr()
	return "http://" + addr, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Stop(ctx)
	}
}

func getAggregate(t *testing.T, base string) AggregateStatusResponse {
	t.Helper()
	resp, err := http.Get(base + "/aggregate")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body: %s", resp.StatusCode, body)
	}
	var out AggregateStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// Bare-bones daemon (no swarm, no lookup) still serves /aggregate
// with a zero-valued payload.
func TestAggregateEndpointEmpty(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	_ = log
	base, stop := startAggregateServer(t, nil, nil)
	defer stop()

	got := getAggregate(t, base)
	if got.PPMIEnabled {
		t.Error("PPMIEnabled should be false with no lookup")
	}
	if got.KnownIndexers != 0 {
		t.Error("KnownIndexers should be 0 with no lookup")
	}
}

// With a Lookup but no PPMI getter, PPMIEnabled stays false.
func TestAggregateEndpointLookupWithoutPPMI(t *testing.T) {
	mem := dhtindex.NewMemoryPutterGetter(nil)
	lookup := dhtindex.NewLookup(mem)
	// Populate one known indexer.
	var pk [32]byte
	pk[0] = 0xAB
	lookup.AddIndexer(pk, "test-anchor")

	base, stop := startAggregateServer(t, nil, lookup)
	defer stop()

	got := getAggregate(t, base)
	if got.PPMIEnabled {
		t.Error("PPMIEnabled should be false without a getter")
	}
	if got.KnownIndexers != 1 {
		t.Errorf("KnownIndexers = %d, want 1", got.KnownIndexers)
	}
	if len(got.Indexers) != 1 {
		t.Fatalf("Indexers = %d, want 1", len(got.Indexers))
	}
	if got.Indexers[0].Label != "test-anchor" {
		t.Errorf("Label = %q, want test-anchor", got.Indexers[0].Label)
	}
	if got.Indexers[0].PubKey != hex.EncodeToString(pk[:]) {
		t.Errorf("PubKey mismatch: %q", got.Indexers[0].PubKey)
	}
}

// With a PPMI getter attached, PPMIEnabled is true.
func TestAggregateEndpointPPMIEnabled(t *testing.T) {
	mem := dhtindex.NewMemoryPutterGetter(nil)
	lookup := dhtindex.NewLookup(mem)
	lookup.SetPPMIGetter(mem)

	base, stop := startAggregateServer(t, nil, lookup)
	defer stop()

	got := getAggregate(t, base)
	if !got.PPMIEnabled {
		t.Error("PPMIEnabled should be true with getter attached")
	}
}

// With a swarm protocol + RecordCache as RecordSource, the
// endpoint reports the cache kind and size.
func TestAggregateEndpointReportsRecordCache(t *testing.T) {
	swarm := swarmsearch.New(nil)
	cache := swarmsearch.NewRecordCache()
	var r swarmsearch.LocalRecord
	r.Pk[0] = 0x01
	r.Kw = "linux"
	r.Ih[0] = 0x10
	r.T = 1
	cache.Add(r)
	cache.Add(swarmsearch.LocalRecord{Pk: r.Pk, Kw: "ubuntu", Ih: [20]byte{0x20}, T: 2})
	swarm.SetRecordSource(cache)

	base, stop := startAggregateServer(t, swarm, nil)
	defer stop()

	got := getAggregate(t, base)
	if got.RecordSourceKind != "cache" {
		t.Errorf("RecordSourceKind = %q, want 'cache'", got.RecordSourceKind)
	}
	if got.RecordCacheSize != 2 {
		t.Errorf("RecordCacheSize = %d, want 2", got.RecordCacheSize)
	}
	// ServicesAdvertised should be a 16-char hex string (uint64 → 8 bytes → 16 hex chars).
	if len(got.ServicesAdvertised) != 16 {
		t.Errorf("ServicesAdvertised len = %d, want 16 (%q)", len(got.ServicesAdvertised), got.ServicesAdvertised)
	}
}

// A non-RecordCache RecordSource is reported as "custom" — the
// endpoint doesn't leak the underlying type name.
type customSource struct{}

func (customSource) LocalRecords(_ swarmsearch.SyncFilter) ([]swarmsearch.LocalRecord, error) {
	return nil, nil
}

func TestAggregateEndpointCustomRecordSource(t *testing.T) {
	swarm := swarmsearch.New(nil)
	swarm.SetRecordSource(customSource{})

	base, stop := startAggregateServer(t, swarm, nil)
	defer stop()

	got := getAggregate(t, base)
	if got.RecordSourceKind != "custom" {
		t.Errorf("RecordSourceKind = %q, want 'custom'", got.RecordSourceKind)
	}
	if got.RecordCacheSize != 0 {
		t.Errorf("RecordCacheSize = %d, want 0 for non-cache source", got.RecordCacheSize)
	}
}

// Bootstrap probe included on the response when attached.
type fakeBootstrap struct {
	anchors  int
	admitted int
	pending  int
}

func (f fakeBootstrap) AnchorCount() int   { return f.anchors }
func (f fakeBootstrap) AdmittedCount() int { return f.admitted }
func (f fakeBootstrap) PendingCount() int  { return f.pending }

func TestAggregateEndpointReportsBootstrap(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewWithOptions("127.0.0.1:0", log, Options{
		Bootstrap: fakeBootstrap{anchors: 5, admitted: 12, pending: 3},
	})
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Stop(ctx)
	}()
	base := "http://" + srv.Addr()

	got := getAggregate(t, base)
	if got.Bootstrap == nil {
		t.Fatal("Bootstrap should be populated when a probe is attached")
	}
	if got.Bootstrap.Anchors != 5 {
		t.Errorf("Anchors = %d, want 5", got.Bootstrap.Anchors)
	}
	if got.Bootstrap.Admitted != 12 {
		t.Errorf("Admitted = %d, want 12", got.Bootstrap.Admitted)
	}
	if got.Bootstrap.Pending != 3 {
		t.Errorf("Pending = %d, want 3", got.Bootstrap.Pending)
	}
}

// Without a probe, the bootstrap block is omitted.
func TestAggregateEndpointOmitsBootstrapWhenNil(t *testing.T) {
	base, stop := startAggregateServer(t, nil, nil)
	defer stop()

	got := getAggregate(t, base)
	if got.Bootstrap != nil {
		t.Errorf("Bootstrap should be omitted when probe is nil, got %+v", got.Bootstrap)
	}
}

// /aggregate returns JSON content-type.
func TestAggregateContentType(t *testing.T) {
	base, stop := startAggregateServer(t, nil, nil)
	defer stop()

	resp, err := http.Get(base + "/aggregate")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}
