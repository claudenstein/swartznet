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
}
