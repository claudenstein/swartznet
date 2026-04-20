package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/httpapi"
	"github.com/swartznet/swartznet/internal/indexer"
)

// TestHTTPSearchLocalErrorReturns500 covers the
// `s.idx.Search err → 500` branch of handleSearch. Opening an
// index then closing it before the HTTP server gets a request
// makes Search return "indexer: closed" — the documented error.
func TestHTTPSearchLocalErrorReturns500(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "soon-closed.bleve"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	base := startServer(t, httpapi.Options{Index: idx})

	// Close the index AFTER the server is wired up but BEFORE we
	// fire the request, so handleSearch hits the error branch.
	if err := idx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	body, _ := json.Marshal(httpapi.SearchRequest{Q: "anything"})
	resp, err := http.Post(base+"/search", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s, want 500", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "local search failed") {
		t.Errorf("response should mention 'local search failed', got %q", raw)
	}
}
