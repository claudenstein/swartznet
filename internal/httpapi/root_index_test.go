package httpapi_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/httpapi"
)

// TestRootServesIndexHTML covers the
// `mux.HandleFunc("GET /{$}", func(...) { http.ServeFileFS(...) })`
// closure body in Server.Start. A bare GET / on the server
// should return the embedded index.html. Without this test the
// closure body itself was unexecuted (the registration alone
// doesn't trip Go coverage; only invoking the handler does).
func TestRootServesIndexHTML(t *testing.T) {
	t.Parallel()
	idx := openTempIndex(t)
	base := startServer(t, httpapi.Options{Index: idx})

	resp, err := http.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	// The embedded index.html must contain the document root tag.
	// Looser than asserting full bytes so cosmetic UI tweaks
	// don't break this test.
	if !strings.Contains(strings.ToLower(string(body)), "<!doctype html>") {
		t.Errorf("body does not look like HTML: %q (first 80 bytes)", string(body[:min(80, len(body))]))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
