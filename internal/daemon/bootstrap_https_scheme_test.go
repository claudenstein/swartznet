package daemon

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestFallbackToHTTPSRejectsNonHTTPSScheme asserts the public
// FallbackToHTTPS entry point fails closed on a plaintext http://
// URL, so an on-path attacker cannot downgrade the anchor fetch and
// inject pubkeys. The fake client is never reached because the
// scheme check runs first.
func TestFallbackToHTTPSRejectsNonHTTPSScheme(t *testing.T) {
	lookup := newTestLookup()
	b, _ := NewBootstrap(lookup, nil, nil, nil, DefaultBootstrapOptions(), nil)

	good := pubkeyBytes("would-be-injected")
	body := []byte(fmt.Sprintf(`{"version":1,"anchors":["%s"]}`,
		hex.EncodeToString(good[:])))

	_, err := b.FallbackToHTTPS(context.Background(),
		"http://attacker.example/v1/anchors", fakeHTTPSClient{body: body})
	if err == nil {
		t.Fatal("expected error for non-https URL")
	}
	if !strings.Contains(err.Error(), "https") {
		t.Errorf("error %q should mention the https requirement", err)
	}
	// The fake client's body must NOT have been ingested.
	if k := b.AnchorKeys(); len(k) != 0 {
		t.Errorf("anchors were added despite http:// rejection: %v", k)
	}
}

// TestRequireHTTPSAcceptsHTTPS covers the accept path: a https://
// URL passes requireHTTPS regardless of the loopback exemption.
func TestRequireHTTPSAcceptsHTTPS(t *testing.T) {
	if err := requireHTTPS("https://bootstrap.example/v1/anchors", false); err != nil {
		t.Fatalf("https URL should be accepted: %v", err)
	}
}

// TestRequireHTTPSLoopbackExemption covers the narrow test-only
// loopback exemption: http://localhost is allowed ONLY when
// allowInsecureLoopback is true, and rejected otherwise.
func TestRequireHTTPSLoopbackExemption(t *testing.T) {
	for _, host := range []string{"localhost", "127.0.0.1", "[::1]"} {
		raw := "http://" + host + ":1234/anchors"
		if err := requireHTTPS(raw, true); err != nil {
			t.Errorf("loopback %q should pass with exemption: %v", raw, err)
		}
		if err := requireHTTPS(raw, false); err == nil {
			t.Errorf("loopback %q must be rejected without exemption", raw)
		}
	}
	// A non-loopback host must be rejected even with the exemption.
	if err := requireHTTPS("http://evil.example/anchors", true); err == nil {
		t.Error("non-loopback http:// must be rejected even with exemption")
	}
}

// TestHTTPGetClientFetchesOverLoopbackHTTP exercises the real
// httpGetClient.Get accept path end-to-end against a plaintext
// httptest loopback server, using the test-only
// allowInsecureLoopback escape hatch. No network beyond loopback.
func TestHTTPGetClientFetchesOverLoopbackHTTP(t *testing.T) {
	want := pubkeyBytes("loopback-anchor")
	body := fmt.Sprintf(`{"version":1,"anchors":["%s"]}`,
		hex.EncodeToString(want[:]))

	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, body)
		}))
	defer srv.Close()

	// Production client (loopback exemption off) must reject the
	// plaintext server URL.
	prod := httpGetClient{c: srv.Client()}
	if _, err := prod.Get(context.Background(), srv.URL); err == nil {
		t.Fatal("production client should reject plaintext loopback URL")
	}

	// Test client with the exemption fetches successfully.
	tc := httpGetClient{c: srv.Client(), allowInsecureLoopback: true}
	got, err := tc.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("loopback Get with exemption: %v", err)
	}
	if string(got) != body {
		t.Errorf("body = %q, want %q", got, body)
	}
}
