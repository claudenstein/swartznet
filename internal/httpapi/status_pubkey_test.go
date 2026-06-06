package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/swartznet/swartznet/internal/dhtindex"
	"github.com/swartznet/swartznet/internal/httpapi"
)

// TestHTTPStatusPublisherPubKey covers the PublisherPubKey probe:
// when a publisher is active and the probe is supplied, /status
// must surface the hex pubkey the web UI renders. Before the fix
// out.Publisher.PubKey was never populated, so this asserted on an
// empty string.
func TestHTTPStatusPublisherPubKey(t *testing.T) {
	t.Parallel()
	mf, err := dhtindex.LoadOrCreateManifest("")
	if err != nil {
		t.Fatal(err)
	}
	pub := dhtindex.NewPublisher(nil, mf, dhtindex.PublisherOptions{}, silentLogger())
	const wantHex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	base := startServer(t, httpapi.Options{
		Publisher:       pub,
		PublisherPubKey: func() string { return wantHex },
	})

	resp, err := http.Get(base + "/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, raw)
	}
	var out httpapi.StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Publisher.PubKey != wantHex {
		t.Errorf("Publisher.PubKey = %q, want %q", out.Publisher.PubKey, wantHex)
	}
}

// TestHTTPStatusPublisherPubKeyOmittedWhenNoProbe confirms the
// field stays empty when no probe is wired (no behavior change for
// callers that do not supply one).
func TestHTTPStatusPublisherPubKeyOmittedWhenNoProbe(t *testing.T) {
	t.Parallel()
	mf, err := dhtindex.LoadOrCreateManifest("")
	if err != nil {
		t.Fatal(err)
	}
	pub := dhtindex.NewPublisher(nil, mf, dhtindex.PublisherOptions{}, silentLogger())
	base := startServer(t, httpapi.Options{Publisher: pub})

	resp, err := http.Get(base + "/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out httpapi.StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Publisher.PubKey != "" {
		t.Errorf("Publisher.PubKey = %q, want empty", out.Publisher.PubKey)
	}
}
