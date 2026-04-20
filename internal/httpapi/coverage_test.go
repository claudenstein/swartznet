package httpapi_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"

	"fmt"

	"github.com/swartznet/swartznet/internal/dhtindex"
	"github.com/swartznet/swartznet/internal/httpapi"
	"github.com/swartznet/swartznet/internal/indexer"
	"github.com/swartznet/swartznet/internal/reputation"
	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// ---------- /healthz ----------

func TestHTTPHealthzVersionField(t *testing.T) {
	// Cannot run in parallel: this test toggles a process-wide
	// version string used by every parallel /healthz caller.
	old := httpapi.HealthzVersion()
	httpapi.SetHealthzVersion("v0.2.0-test")
	t.Cleanup(func() { httpapi.SetHealthzVersion(old) })

	base := startServer(t, httpapi.Options{})
	resp, err := http.Get(base + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
	var body struct {
		OK      bool   `json:"ok"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.OK {
		t.Error("ok=false, want true")
	}
	if body.Version != "v0.2.0-test" {
		t.Errorf("version=%q, want v0.2.0-test", body.Version)
	}
}

// ---------- /status with all collaborators ----------

func TestHTTPStatusAllComponents(t *testing.T) {
	t.Parallel()

	idx := openTempIndex(t)
	swarm := swarmsearch.New(silentLogger())
	bloom := reputation.NewBloomFilter(1000, 0.01)
	tracker := reputation.NewTracker()

	// Seed the tracker with a known indexer so the status
	// response has a non-empty reputation section.
	tracker.RecordReturned("pk_aaa", 10)
	tracker.RecordConfirmed("pk_aaa", "pk_aaa", "pk_aaa")

	base := startServer(t, httpapi.Options{
		Index:   idx,
		Swarm:   swarm,
		Bloom:   bloom,
		Tracker: tracker,
	})

	resp, err := http.Get(base + "/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}

	var out httpapi.StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}

	// Local section
	if !out.Local.Indexed {
		t.Error("Local.Indexed = false, want true")
	}
	if out.Local.DocCount == 0 {
		t.Error("Local.DocCount = 0, want non-zero")
	}

	// Swarm section (protocol has no peers, but the fields should exist)
	if out.Swarm.KnownPeers != 0 {
		t.Errorf("Swarm.KnownPeers = %d, want 0", out.Swarm.KnownPeers)
	}
	if out.Swarm.CapablePeers != 0 {
		t.Errorf("Swarm.CapablePeers = %d, want 0", out.Swarm.CapablePeers)
	}

	// Bloom section
	if out.Bloom == nil {
		t.Fatal("Bloom is nil, want non-nil")
	}
	if out.Bloom.BitSize == 0 {
		t.Error("Bloom.BitSize = 0, want positive")
	}
	if out.Bloom.HashFunctions == 0 {
		t.Error("Bloom.HashFunctions = 0, want positive")
	}

	// Reputation section
	if out.Reputation == nil {
		t.Fatal("Reputation is nil, want non-nil")
	}
	if out.Reputation.KnownIndexers != 1 {
		t.Errorf("Reputation.KnownIndexers = %d, want 1", out.Reputation.KnownIndexers)
	}
	if len(out.Reputation.TopIndexers) != 1 {
		t.Fatalf("TopIndexers len = %d, want 1", len(out.Reputation.TopIndexers))
	}
	if out.Reputation.TopIndexers[0].PubKey != "pk_aaa" {
		t.Errorf("TopIndexers[0].PubKey = %q, want pk_aaa", out.Reputation.TopIndexers[0].PubKey)
	}
	if out.Reputation.TopIndexers[0].HitsReturned != 10 {
		t.Errorf("TopIndexers[0].HitsReturned = %d, want 10", out.Reputation.TopIndexers[0].HitsReturned)
	}
	if out.Reputation.TopIndexers[0].HitsConfirmed != 3 {
		t.Errorf("TopIndexers[0].HitsConfirmed = %d, want 3", out.Reputation.TopIndexers[0].HitsConfirmed)
	}
}

func TestHTTPStatusWithPublisher(t *testing.T) {
	t.Parallel()

	manifest, err := dhtindex.LoadOrCreateManifest("")
	if err != nil {
		t.Fatal(err)
	}
	// Add a keyword with a hit to the manifest so Status()
	// returns non-empty publisher data.
	_, err = manifest.AddHit("ubuntu", dhtindex.KeywordHit{
		IH: []byte{0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11,
			0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11},
		N: "Ubuntu 24.04",
	})
	if err != nil {
		t.Fatal(err)
	}

	pub := dhtindex.NewPublisher(nil, manifest, dhtindex.PublisherOptions{}, silentLogger())

	base := startServer(t, httpapi.Options{Publisher: pub})

	resp, err := http.Get(base + "/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}

	var out httpapi.StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Publisher.TotalKeywords != 1 {
		t.Errorf("Publisher.TotalKeywords=%d, want 1", out.Publisher.TotalKeywords)
	}
	if out.Publisher.TotalHits != 1 {
		t.Errorf("Publisher.TotalHits=%d, want 1", out.Publisher.TotalHits)
	}
	if len(out.Publisher.Keywords) != 1 {
		t.Fatalf("Publisher.Keywords len=%d, want 1", len(out.Publisher.Keywords))
	}
	if out.Publisher.Keywords[0].Keyword != "ubuntu" {
		t.Errorf("Publisher.Keywords[0].Keyword=%q, want ubuntu", out.Publisher.Keywords[0].Keyword)
	}
}

func TestHTTPStatusReputationTruncatesTop10(t *testing.T) {
	t.Parallel()
	tracker := reputation.NewTracker()
	// Seed 15 indexers so the top-10 truncation path is exercised.
	for i := 0; i < 15; i++ {
		pk := reputation.PubKeyHex(fmt.Sprintf("pk_%02d", i))
		tracker.RecordReturned(pk, 10+i)
	}

	base := startServer(t, httpapi.Options{Tracker: tracker})

	resp, err := http.Get(base + "/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	var out httpapi.StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Reputation == nil {
		t.Fatal("Reputation is nil")
	}
	if out.Reputation.KnownIndexers != 15 {
		t.Errorf("KnownIndexers=%d, want 15", out.Reputation.KnownIndexers)
	}
	if len(out.Reputation.TopIndexers) != 10 {
		t.Errorf("TopIndexers len=%d, want 10 (truncated)", len(out.Reputation.TopIndexers))
	}
}

func TestHTTPStatusNoCollaborators(t *testing.T) {
	t.Parallel()
	base := startServer(t, httpapi.Options{})

	resp, err := http.Get(base + "/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
	var out httpapi.StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Local.Indexed {
		t.Error("Local.Indexed = true, want false (no index configured)")
	}
	if out.Bloom != nil {
		t.Error("Bloom should be nil when bloom is not configured")
	}
	if out.Reputation != nil {
		t.Error("Reputation should be nil when tracker is not configured")
	}
}

// ---------- /confirm with bloom filter ----------

func TestHTTPConfirmAddsToBloom(t *testing.T) {
	t.Parallel()
	bloom := reputation.NewBloomFilter(1000, 0.01)
	base := startServer(t, httpapi.Options{Bloom: bloom})

	ih := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	body, _ := json.Marshal(map[string]string{"infohash": ih})
	resp, err := http.Post(base+"/confirm", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}

	var out httpapi.FlagResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !out.OK {
		t.Error("OK=false, want true")
	}
	if out.InfoHash != ih {
		t.Errorf("InfoHash=%q, want %q", out.InfoHash, ih)
	}

	// Verify the infohash was actually added to the bloom filter.
	raw, _ := hex.DecodeString(ih)
	if !bloom.Test(raw) {
		t.Error("bloom.Test returned false after confirm")
	}
}

func TestHTTPConfirmBloomNotConfigured(t *testing.T) {
	t.Parallel()
	base := startServer(t, httpapi.Options{})

	body := []byte(`{"infohash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
	resp, err := http.Post(base+"/confirm", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status=%d, want 503", resp.StatusCode)
	}
}

func TestHTTPConfirmShortInfohash(t *testing.T) {
	t.Parallel()
	bloom := reputation.NewBloomFilter(1000, 0.01)
	base := startServer(t, httpapi.Options{Bloom: bloom})

	// 38 hex chars — too short (need 40 = 20 bytes).
	body := []byte(`{"infohash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
	resp, err := http.Post(base+"/confirm", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", resp.StatusCode)
	}
}

// ---------- /flag with tracker + source attribution ----------

func TestHTTPFlagWithSourceAttribution(t *testing.T) {
	t.Parallel()
	tracker := reputation.NewTracker()
	sources := reputation.NewSourceTracker(0)

	// Simulate that indexer "pk_bad" returned this infohash.
	ih := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	tracker.RecordReturned("pk_bad", 5)
	sources.Record(ih, "pk_bad")

	base := startServer(t, httpapi.Options{
		Tracker: tracker,
		Sources: sources,
	})

	body, _ := json.Marshal(map[string]string{"infohash": ih})
	resp, err := http.Post(base+"/flag", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}

	var out httpapi.FlagResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !out.OK {
		t.Error("OK=false, want true")
	}

	// The targeted indexer should have been demoted.
	snap := tracker.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("snapshot len=%d, want 1", len(snap))
	}
	if snap[0].Counters.HitsFlagged != 1 {
		t.Errorf("HitsFlagged=%d, want 1", snap[0].Counters.HitsFlagged)
	}

	// Source should have been forgotten after flagging.
	if srcs := sources.Sources(ih); len(srcs) != 0 {
		t.Errorf("sources after flag = %v, want empty", srcs)
	}
}

func TestHTTPFlagFallbackNoSources(t *testing.T) {
	t.Parallel()
	tracker := reputation.NewTracker()
	// Two known indexers, but no source attribution for the hash.
	tracker.RecordReturned("pk_a", 3)
	tracker.RecordReturned("pk_b", 7)

	base := startServer(t, httpapi.Options{Tracker: tracker})

	ih := "cccccccccccccccccccccccccccccccccccccccc"
	body, _ := json.Marshal(map[string]string{"infohash": ih})
	resp, err := http.Post(base+"/flag", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}

	// Both indexers should have been flagged (fallback path).
	snap := tracker.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("snapshot len=%d, want 2", len(snap))
	}
	for _, entry := range snap {
		if entry.Counters.HitsFlagged != 1 {
			t.Errorf("indexer %s HitsFlagged=%d, want 1", entry.PubKey, entry.Counters.HitsFlagged)
		}
	}
}

func TestHTTPFlagTrackerNotConfigured(t *testing.T) {
	t.Parallel()
	base := startServer(t, httpapi.Options{})

	body := []byte(`{"infohash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
	resp, err := http.Post(base+"/flag", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status=%d, want 503", resp.StatusCode)
	}
}

func TestHTTPFlagBadJSON(t *testing.T) {
	t.Parallel()
	tracker := reputation.NewTracker()
	base := startServer(t, httpapi.Options{Tracker: tracker})

	resp, err := http.Post(base+"/flag", "application/json", bytes.NewReader([]byte("{bad")))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", resp.StatusCode)
	}
}

func TestHTTPFlagBadInfohash(t *testing.T) {
	t.Parallel()
	tracker := reputation.NewTracker()
	base := startServer(t, httpapi.Options{Tracker: tracker})

	resp, err := http.Post(base+"/flag", "application/json",
		bytes.NewReader([]byte(`{"infohash":"zzzz"}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", resp.StatusCode)
	}
}

// ---------- /capabilities GET + POST ----------

func TestHTTPGetCapabilities(t *testing.T) {
	t.Parallel()
	swarm := swarmsearch.New(silentLogger())
	base := startServer(t, httpapi.Options{Swarm: swarm})

	resp, err := http.Get(base + "/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	var caps httpapi.CapabilitiesBody
	if err := json.NewDecoder(resp.Body).Decode(&caps); err != nil {
		t.Fatal(err)
	}
	// Default capabilities from swarmsearch.DefaultCapabilities()
	if caps.ShareLocal != 2 {
		t.Errorf("ShareLocal=%d, want 2", caps.ShareLocal)
	}
	if caps.FileHits != 1 {
		t.Errorf("FileHits=%d, want 1", caps.FileHits)
	}
	if caps.ContentHits != 1 {
		t.Errorf("ContentHits=%d, want 1", caps.ContentHits)
	}
	if caps.Publisher != 0 {
		t.Errorf("Publisher=%d, want 0", caps.Publisher)
	}
}

func TestHTTPSetCapabilitiesClampHigh(t *testing.T) {
	t.Parallel()
	swarm := swarmsearch.New(silentLogger())
	base := startServer(t, httpapi.Options{Swarm: swarm})

	// Send values above the valid ranges; all should be clamped.
	body, _ := json.Marshal(httpapi.CapabilitiesBody{
		ShareLocal:  99,  // max 2
		FileHits:    10,  // max 1
		ContentHits: 5,   // max 1
		Publisher:   100, // max 1
	})
	resp, err := http.Post(base+"/capabilities", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}

	var caps httpapi.CapabilitiesBody
	if err := json.NewDecoder(resp.Body).Decode(&caps); err != nil {
		t.Fatal(err)
	}
	if caps.ShareLocal != 2 {
		t.Errorf("ShareLocal=%d, want 2 (clamped from 99)", caps.ShareLocal)
	}
	if caps.FileHits != 1 {
		t.Errorf("FileHits=%d, want 1 (clamped from 10)", caps.FileHits)
	}
	if caps.ContentHits != 1 {
		t.Errorf("ContentHits=%d, want 1 (clamped from 5)", caps.ContentHits)
	}
	if caps.Publisher != 1 {
		t.Errorf("Publisher=%d, want 1 (clamped from 100)", caps.Publisher)
	}
}

func TestHTTPSetCapabilitiesClampLow(t *testing.T) {
	t.Parallel()
	swarm := swarmsearch.New(silentLogger())
	base := startServer(t, httpapi.Options{Swarm: swarm})

	// Send negative values; all should be clamped to 0.
	body, _ := json.Marshal(httpapi.CapabilitiesBody{
		ShareLocal:  -5,
		FileHits:    -1,
		ContentHits: -10,
		Publisher:   -3,
	})
	resp, err := http.Post(base+"/capabilities", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}

	var caps httpapi.CapabilitiesBody
	if err := json.NewDecoder(resp.Body).Decode(&caps); err != nil {
		t.Fatal(err)
	}
	if caps.ShareLocal != 0 {
		t.Errorf("ShareLocal=%d, want 0 (clamped from -5)", caps.ShareLocal)
	}
	if caps.FileHits != 0 {
		t.Errorf("FileHits=%d, want 0 (clamped from -1)", caps.FileHits)
	}
	if caps.ContentHits != 0 {
		t.Errorf("ContentHits=%d, want 0 (clamped from -10)", caps.ContentHits)
	}
	if caps.Publisher != 0 {
		t.Errorf("Publisher=%d, want 0 (clamped from -3)", caps.Publisher)
	}
}

func TestHTTPSetCapabilitiesValidValues(t *testing.T) {
	t.Parallel()
	swarm := swarmsearch.New(silentLogger())
	base := startServer(t, httpapi.Options{Swarm: swarm})

	// Send valid values within range; should pass through unchanged.
	body, _ := json.Marshal(httpapi.CapabilitiesBody{
		ShareLocal:  1,
		FileHits:    1,
		ContentHits: 0,
		Publisher:   1,
	})
	resp, err := http.Post(base+"/capabilities", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}

	var caps httpapi.CapabilitiesBody
	if err := json.NewDecoder(resp.Body).Decode(&caps); err != nil {
		t.Fatal(err)
	}
	if caps.ShareLocal != 1 {
		t.Errorf("ShareLocal=%d, want 1", caps.ShareLocal)
	}
	if caps.FileHits != 1 {
		t.Errorf("FileHits=%d, want 1", caps.FileHits)
	}
	if caps.ContentHits != 0 {
		t.Errorf("ContentHits=%d, want 0", caps.ContentHits)
	}
	if caps.Publisher != 1 {
		t.Errorf("Publisher=%d, want 1", caps.Publisher)
	}
}

func TestHTTPSetCapabilitiesBadJSON(t *testing.T) {
	t.Parallel()
	swarm := swarmsearch.New(silentLogger())
	base := startServer(t, httpapi.Options{Swarm: swarm})

	resp, err := http.Post(base+"/capabilities", "application/json",
		bytes.NewReader([]byte("{not json")))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", resp.StatusCode)
	}
}

func TestHTTPSetCapabilitiesSwarmNotConfigured(t *testing.T) {
	t.Parallel()
	base := startServer(t, httpapi.Options{})

	body, _ := json.Marshal(httpapi.CapabilitiesBody{ShareLocal: 1})
	resp, err := http.Post(base+"/capabilities", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status=%d, want 503", resp.StatusCode)
	}
}

// ---------- /search edge cases ----------

func TestHTTPSearchBadJSON(t *testing.T) {
	t.Parallel()
	base := startServer(t, httpapi.Options{})

	resp, err := http.Post(base+"/search", "application/json",
		bytes.NewReader([]byte("{bad json")))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", resp.StatusCode)
	}
}

func TestHTTPSearchNoIndex(t *testing.T) {
	t.Parallel()
	// Search with no index configured should still return 200
	// with an empty local result.
	base := startServer(t, httpapi.Options{})

	body, _ := json.Marshal(httpapi.SearchRequest{Q: "hello"})
	resp, err := http.Post(base+"/search", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}

	var out httpapi.SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Local.Total != 0 {
		t.Errorf("Local.Total=%d, want 0", out.Local.Total)
	}
	if len(out.Local.Hits) != 0 {
		t.Errorf("Local.Hits len=%d, want 0", len(out.Local.Hits))
	}
}

func TestHTTPSearchDefaultLimit(t *testing.T) {
	t.Parallel()
	idx := openTempIndex(t)
	base := startServer(t, httpapi.Options{Index: idx})

	// Limit=0 should default to 50, not cause an error.
	body, _ := json.Marshal(httpapi.SearchRequest{Q: "ubuntu", Limit: 0})
	resp, err := http.Post(base+"/search", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}

	var out httpapi.SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Local.Total != 1 {
		t.Errorf("Local.Total=%d, want 1", out.Local.Total)
	}
}

func TestHTTPSearchSwarmRequestedButNilProtocol(t *testing.T) {
	t.Parallel()
	// When swarm=true but no protocol is configured, the handler
	// should silently skip swarm and not crash.
	idx := openTempIndex(t)
	base := startServer(t, httpapi.Options{Index: idx})

	body, _ := json.Marshal(httpapi.SearchRequest{Q: "ubuntu", Swarm: true})
	resp, err := http.Post(base+"/search", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}

	var out httpapi.SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	// Swarm was requested but protocol is nil => swarm section omitted.
	if out.Swarm != nil {
		t.Errorf("Swarm should be nil when protocol is nil, got %+v", out.Swarm)
	}
}

func TestHTTPSearchDHTRequestedButNilLookup(t *testing.T) {
	t.Parallel()
	// When dht=true but no lookup is configured, the handler
	// should silently skip DHT and not crash.
	idx := openTempIndex(t)
	base := startServer(t, httpapi.Options{Index: idx})

	body, _ := json.Marshal(httpapi.SearchRequest{Q: "ubuntu", DHT: true})
	resp, err := http.Post(base+"/search", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}

	var out httpapi.SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	// DHT was requested but lookup is nil => dht section omitted.
	if out.DHT != nil {
		t.Errorf("DHT should be nil when lookup is nil, got %+v", out.DHT)
	}
}

func TestHTTPSearchWithHighlight(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "idx"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	if err := idx.IndexContent(indexer.ContentDoc{
		InfoHash: "2222222222222222222222222222222222222222",
		FilePath: "chapter1.txt",
		Text:     "the quick brown fox jumps over the lazy dog",
	}); err != nil {
		t.Fatal(err)
	}

	base := startServer(t, httpapi.Options{Index: idx})

	body, _ := json.Marshal(httpapi.SearchRequest{
		Q:         "brown fox",
		Highlight: true,
	})
	resp, err := http.Post(base+"/search", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}

	var out httpapi.SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Local.Total == 0 {
		t.Fatal("expected at least 1 hit")
	}
	// When Highlight is true, the response should include fragments.
	hit := out.Local.Hits[0]
	if len(hit.Fragments) == 0 {
		t.Error("Fragments is empty, expected highlighted fragments")
	}
}

// ---------- /torrent (add) edge cases ----------

func TestHTTPAddTorrentAdderNotConfigured(t *testing.T) {
	t.Parallel()
	base := startServer(t, httpapi.Options{})

	body := []byte(`{"uri":"magnet:?xt=urn:btih:1111111111111111111111111111111111111111"}`)
	resp, err := http.Post(base+"/torrent", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status=%d, want 503", resp.StatusCode)
	}
}

func TestHTTPAddTorrentAdderError(t *testing.T) {
	t.Parallel()
	fc := &fakeController{addErr: errForTest("bad magnet")}
	base := startServer(t, httpapi.Options{Adder: fc})

	body := []byte(`{"uri":"magnet:?xt=urn:btih:badhash"}`)
	resp, err := http.Post(base+"/torrent", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", resp.StatusCode)
	}
}

// ---------- /torrents list unconfigured ----------

func TestHTTPListTorrentsUnconfigured(t *testing.T) {
	t.Parallel()
	base := startServer(t, httpapi.Options{})

	resp, err := http.Get(base + "/torrents")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status=%d, want 503", resp.StatusCode)
	}
}

// ---------- /torrents control with bad infohash format ----------

func TestHTTPResumeDeleteBadInfohash(t *testing.T) {
	t.Parallel()
	fc := &fakeController{}
	base := startServer(t, httpapi.Options{Control: fc})

	// Resume with short hex
	resp, err := http.Post(base+"/torrents/tooshort/resume", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("resume short: status=%d, want 400", resp.StatusCode)
	}

	// Delete with bad hex
	req, _ := http.NewRequest("DELETE", base+"/torrents/gggggggggggggggggggggggggggggggggggggggg", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("delete bad hex: status=%d, want 400", resp.StatusCode)
	}
}

// errForTest is a trivial error type for test stubs.
type errForTest string

func (e errForTest) Error() string { return string(e) }
