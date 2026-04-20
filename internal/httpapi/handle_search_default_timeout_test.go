package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/swartznet/swartznet/internal/httpapi"
	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// TestHTTPSearchSwarmDefaultTimeout covers the
// `timeout == 0 → timeout = 2 * time.Second` default branch of
// handleSearch. The existing swarm-configured test passes
// SwarmTimeoutMs explicitly; omitting it should hit the
// default-to-2-seconds substitute. With zero peers the swarm
// Query returns quickly, so the test doesn't actually wait 2s.
func TestHTTPSearchSwarmDefaultTimeout(t *testing.T) {
	t.Parallel()
	swarm := swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	idx := openTempIndex(t)
	base := startServer(t, httpapi.Options{Index: idx, Swarm: swarm})

	// Note: SwarmTimeoutMs deliberately omitted (zero).
	body, _ := json.Marshal(httpapi.SearchRequest{
		Q:     "ubuntu",
		Swarm: true,
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
	if got.Swarm == nil {
		t.Fatal("Swarm result should not be nil when protocol is configured")
	}
}
