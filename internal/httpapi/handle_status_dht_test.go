package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/httpapi"
)

// TestHTTPStatusDHTField covers the DHT block of /status.
//
//   - With httpapi.Options.DHTStats supplied, the JSON body has
//     a "dht" object populated from the callback. This is the
//     path the daemon wires up in production (calling
//     engine.DHTRoutingTableSize).
//   - With DHTStats nil, the "dht" field is omitted entirely —
//     no leakage of fake "0/0" data for daemons started with
//     DisableDHT=true.
func TestHTTPStatusDHTField(t *testing.T) {
	t.Parallel()

	t.Run("populated_when_wired", func(t *testing.T) {
		t.Parallel()
		stats := func() (good, total int) { return 17, 42 }
		base := startServer(t, httpapi.Options{DHTStats: stats})

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
		if out.DHT == nil {
			t.Fatalf("DHT field omitted despite DHTStats being wired")
		}
		if out.DHT.GoodNodes != 17 || out.DHT.Nodes != 42 {
			t.Errorf("DHT = %+v, want {GoodNodes:17 Nodes:42}", out.DHT)
		}
	})

	t.Run("omitted_when_nil", func(t *testing.T) {
		t.Parallel()
		base := startServer(t, httpapi.Options{})

		resp, err := http.Get(base + "/status")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)

		// Structural check: the "dht" key must NOT appear in the
		// JSON output. Unmarshalling and checking .DHT == nil
		// would also work but wouldn't catch a mistakenly-emitted
		// empty object.
		if strings.Contains(string(raw), `"dht"`) {
			t.Errorf("\"dht\" field should be omitted when no DHTStats wired; body=%s", raw)
		}
	})
}
