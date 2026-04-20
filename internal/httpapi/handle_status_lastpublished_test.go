package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/dhtindex"
	"github.com/swartznet/swartznet/internal/httpapi"
)

// TestHTTPStatusLastPublishedFormatted covers the
// `!ks.LastPublished.IsZero() → entry.LastPublished` branch of
// handleStatus. The existing publisher-status test populates a
// manifest entry but never marks it published; pre-mark one so
// the formatted RFC3339 timestamp is emitted.
func TestHTTPStatusLastPublishedFormatted(t *testing.T) {
	t.Parallel()
	mf, err := dhtindex.LoadOrCreateManifest("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mf.AddHit("ubuntu", dhtindex.KeywordHit{
		IH: []byte{0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11,
			0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11},
		N: "Ubuntu 24.04",
	}); err != nil {
		t.Fatal(err)
	}
	// Force a non-zero LastPublished so the formatter branch runs.
	mf.MarkPublished("ubuntu", time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC))

	pub := dhtindex.NewPublisher(nil, mf, dhtindex.PublisherOptions{}, silentLogger())
	base := startServer(t, httpapi.Options{Publisher: pub})

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
	if len(out.Publisher.Keywords) != 1 {
		t.Fatalf("Keywords len = %d, want 1", len(out.Publisher.Keywords))
	}
	got := out.Publisher.Keywords[0].LastPublished
	if !strings.HasPrefix(got, "2026-04-20T12:00:00") {
		t.Errorf("LastPublished = %q, want to start with '2026-04-20T12:00:00'", got)
	}
}
