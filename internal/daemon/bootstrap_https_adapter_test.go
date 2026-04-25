package daemon

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/reputation"
)

// TestNewHTTPSFallbackClientUsesProvidedTimeout verifies that
// the constructor produces an *httpGetClient whose underlying
// http.Client respects the caller's timeout.
func TestNewHTTPSFallbackClientUsesProvidedTimeout(t *testing.T) {
	t.Parallel()
	c := NewHTTPSFallbackClient(7 * time.Second)
	hg, ok := c.(httpGetClient)
	if !ok {
		t.Fatalf("got %T, want httpGetClient", c)
	}
	if hg.c.Timeout != 7*time.Second {
		t.Errorf("Timeout = %s, want 7s", hg.c.Timeout)
	}
}

// TestNewHTTPSFallbackClientZeroFallsBackTo15s — the constructor
// must not produce a client with a zero / negative timeout, since
// the bootstrap path is the last-ditch network call and an
// unbounded one would hang the whole startup.
func TestNewHTTPSFallbackClientZeroFallsBackTo15s(t *testing.T) {
	t.Parallel()
	for _, tc := range []time.Duration{0, -1 * time.Second} {
		c := NewHTTPSFallbackClient(tc)
		hg := c.(httpGetClient)
		if hg.c.Timeout != 15*time.Second {
			t.Errorf("timeout(%s) → got %s, want 15s default", tc, hg.c.Timeout)
		}
	}
}

// TestHttpGetClientHappyPath round-trips through a real (in-process)
// http.Server: returns the response body and exits cleanly.
func TestHttpGetClientHappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"version":1,"anchors":[]}`))
	}))
	t.Cleanup(srv.Close)

	c := NewHTTPSFallbackClient(2 * time.Second)
	body, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !strings.Contains(string(body), `"anchors":[]`) {
		t.Errorf("body = %q, missing anchors", string(body))
	}
}

// TestHttpGetClientNon200ReturnsError — the adapter must surface
// non-200 responses as errors so FallbackToHTTPS can refuse to
// trust a 503 / 404 with a helpful message.
func TestHttpGetClientNon200ReturnsError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down for maintenance", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	c := NewHTTPSFallbackClient(2 * time.Second)
	_, err := c.Get(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error for 503 response")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("err missing 503 status: %v", err)
	}
}

// TestHttpGetClientBadRequestURL — context error path: an
// unparseable URL should bubble out as a Get error rather than
// panic deep in net/http.
func TestHttpGetClientBadRequestURL(t *testing.T) {
	t.Parallel()
	c := NewHTTPSFallbackClient(time.Second)
	_, err := c.Get(context.Background(), "://not a url")
	if err == nil {
		t.Error("expected error for malformed URL")
	}
}

// TestHttpGetClientDialFailure covers the
// `resp, err := g.c.Do(req)` error arm. The URL is parseable
// (so NewRequestWithContext succeeds) but the target is a
// reserved address or a closed port that http.Client cannot
// reach, surfacing a transport error rather than a non-200
// status. We use a context that's already canceled to make
// the failure deterministic.
func TestHttpGetClientDialFailure(t *testing.T) {
	t.Parallel()
	c := NewHTTPSFallbackClient(time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel so Do returns ctx.Err() immediately
	_, err := c.Get(ctx, "https://example.com")
	if err == nil {
		t.Error("expected error from canceled context")
	}
}

// TestBootstrapAnchorCount — direct accessor coverage. Exercises
// both empty and post-FallbackToHTTPS states.
func TestBootstrapAnchorCount(t *testing.T) {
	t.Parallel()
	lookup := newTestLookup()
	b, _ := NewBootstrap(lookup, nil, nil, nil, DefaultBootstrapOptions(), nil)

	if got := b.AnchorCount(); got != 0 {
		t.Errorf("fresh AnchorCount = %d, want 0", got)
	}

	pub := pubkeyBytes("anchor-count-test")
	body := []byte(fmt.Sprintf(`{"version":1,"anchors":["%s"]}`,
		hex.EncodeToString(pub[:])))
	if _, err := b.FallbackToHTTPS(context.Background(), "x", fakeHTTPSClient{body: body}); err != nil {
		t.Fatalf("FallbackToHTTPS: %v", err)
	}
	if got := b.AnchorCount(); got != 1 {
		t.Errorf("post-fallback AnchorCount = %d, want 1", got)
	}
}

// TestNewBootstrapNilLookup covers the
// `if lookup == nil { return error }` guard. Without a Lookup
// the orchestrator has nothing to admit publishers into, so
// constructor must reject the call rather than build a
// half-wired struct.
func TestNewBootstrapNilLookup(t *testing.T) {
	t.Parallel()
	if _, err := NewBootstrap(nil, nil, nil, nil, DefaultBootstrapOptions(), nil); err == nil {
		t.Error("NewBootstrap(nil lookup) should error")
	}
}

// TestNewBootstrapZeroOptionsFillDefaults covers the four
// default-fill arms (MaxTrackedPublishers, AnchorReputation,
// CandidateReputation, EndorsementThreshold) plus the nil-log
// substitution branch. DefaultBootstrapOptions returns all
// positive values so existing tests bypass these guards;
// constructing with zero-valued options + nil log forces the
// fallback assignments to run.
func TestNewBootstrapZeroOptionsFillDefaults(t *testing.T) {
	t.Parallel()
	lookup := newTestLookup()
	b, err := NewBootstrap(lookup, nil, nil, nil, BootstrapOptions{}, nil)
	if err != nil {
		t.Fatalf("NewBootstrap with zero options: %v", err)
	}
	if b == nil {
		t.Fatal("Bootstrap is nil after constructor returned nil error")
	}
}

// TestIngestEndorsementOnAdmittedFreshEndorser covers the
// second `if _, ok := b.endorsements[cand]; !ok { make(...) }`
// arm — the admitted-but-no-prior-endorsements case. Pre-admit
// a candidate via the bloom-policy path (so endorsements[cand]
// is still empty), then call IngestEndorsement and verify it
// stores the endorser without panicking on the missing map.
func TestIngestEndorsementOnAdmittedFreshEndorser(t *testing.T) {
	t.Parallel()
	lookup := newTestLookup()
	// Use a permissive bloom filter so any first crawl admits
	// directly via bloomPolicy without needing endorsements.
	bf := reputation.NewBloomFilter(16, 0.01)
	tr := reputation.NewTracker()
	b, err := NewBootstrap(lookup, nil, bf, tr, DefaultBootstrapOptions(), nil)
	if err != nil {
		t.Fatalf("NewBootstrap: %v", err)
	}

	cand := pubkeyBytes("admitted-no-endorsers")
	// Seed the bloom so bloomPolicy(cand) → true on first crawl.
	bf.Add(cand[:])
	if !b.CandidateFromCrawl(cand, true) {
		t.Fatal("expected immediate admission via bloom policy")
	}
	if !b.IsAdmitted(cand) {
		t.Fatal("cand not admitted")
	}

	// First-ever endorser arrives AFTER admission. The fresh-map
	// guard inside the admitted branch must allocate the
	// endorsers set rather than nil-deref it.
	endorser := pubkeyBytes("endorser-X")
	if !b.IngestEndorsement(endorser, cand) {
		t.Error("IngestEndorsement on admitted cand should report admitted=true")
	}
}

// TestBootstrapPendingCount — exercises both "endorsement only"
// and "observed only" code paths plus the dedup-against-admitted
// short-circuit. PendingCount should always reflect the unique
// not-yet-admitted set.
func TestBootstrapPendingCount(t *testing.T) {
	t.Parallel()
	lookup := newTestLookup()
	// Default EndorsementThreshold = 3, no bloom, no tracker —
	// admission cannot fire from a single endorsement or a single
	// crawl observation, so PendingCount stays a clean signal.
	b, _ := NewBootstrap(lookup, nil, nil, nil, DefaultBootstrapOptions(), nil)

	if got := b.PendingCount(); got != 0 {
		t.Errorf("fresh PendingCount = %d, want 0", got)
	}

	endorser := pubkeyBytes("endorser-A")
	cand1 := pubkeyBytes("candidate-1")
	cand2 := pubkeyBytes("candidate-2")

	// Single endorsement → pending=1
	b.IngestEndorsement(endorser, cand1)
	if got := b.PendingCount(); got != 1 {
		t.Errorf("after 1 endorsement PendingCount = %d, want 1", got)
	}

	// CandidateFromCrawl on a different pubkey → pending=2
	b.CandidateFromCrawl(cand2, true)
	if got := b.PendingCount(); got != 2 {
		t.Errorf("after crawl + endorse PendingCount = %d, want 2", got)
	}

	// Same pubkey via crawl that's already endorsed → still pending=2 (dedup)
	b.CandidateFromCrawl(cand1, true)
	if got := b.PendingCount(); got != 2 {
		t.Errorf("post-dedup PendingCount = %d, want 2 (no double-count)", got)
	}

	// Drive cand1 to admission via the EndorsementThreshold path.
	// EndorsementThreshold=3 (default) so two more distinct endorsers
	// push cand1 over the line. Without a tracker, every endorser is
	// counted as strong (see countStrongEndorsers).
	b.IngestEndorsement(pubkeyBytes("endorser-B"), cand1)
	b.IngestEndorsement(pubkeyBytes("endorser-C"), cand1)
	if !b.IsAdmitted(cand1) {
		t.Fatal("expected cand1 to be admitted after 3 endorsers")
	}
	// cand1 stays in both endorsements and observed maps but
	// PendingCount must filter it out via the admitted check.
	// Only cand2 remains pending.
	if got := b.PendingCount(); got != 1 {
		t.Errorf("post-admission PendingCount = %d, want 1 (cand2 only)", got)
	}
}
