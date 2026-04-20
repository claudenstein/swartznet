package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/swartznet/swartznet/internal/dhtindex"
	"github.com/swartznet/swartznet/internal/httpapi"
)

// TestHTTPSearchDHTDefaultTimeout covers the
// `DHTTimeoutMs == 0 → timeout = 5 * time.Second` default
// branch of handleSearch. The existing DHT-configured test
// passes DHTTimeoutMs explicitly; omitting it should hit the
// default-to-5-seconds substitute.
func TestHTTPSearchDHTDefaultTimeout(t *testing.T) {
	t.Parallel()
	// An empty getter yields IndexersAsked=0; the Query returns quickly.
	getter := dhtindex.NewMemoryPutterGetter(nil)
	lookup := dhtindex.NewLookup(getter)
	idx := openTempIndex(t)
	base := startServer(t, httpapi.Options{Index: idx, Lookup: lookup})

	// Note: DHTTimeoutMs deliberately omitted (zero).
	body, _ := json.Marshal(httpapi.SearchRequest{
		Q:   "ubuntu",
		DHT: true,
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
}
