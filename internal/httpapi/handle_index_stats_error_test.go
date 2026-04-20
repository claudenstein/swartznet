package httpapi_test

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/httpapi"
	"github.com/swartznet/swartznet/internal/indexer"
)

// TestHTTPIndexStatsErrorReturns500 covers the
// `s.idx.Stats() err → 500` branch of handleIndexStats. Open
// an index, attach it to the server, then close it before the
// request fires; the resulting "indexer: closed" error should
// surface as a 500 with a "stats:" prefix.
func TestHTTPIndexStatsErrorReturns500(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "soon-closed.bleve"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	base := startServer(t, httpapi.Options{Index: idx})

	if err := idx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	resp, err := http.Get(base + "/index/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s, want 500", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "stats:") {
		t.Errorf("response should mention 'stats:', got %q", raw)
	}
}
