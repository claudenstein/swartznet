package daemon_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/swartznet/swartznet/internal/daemon"
)

// TestHTTPAPIAddListDeleteCycle drives the daemon's HTTP API
// through a realistic add → list → delete cycle the way the
// web UI would. The existing http_reachability test only asserts
// that /healthz stays up; nothing covers the actual CRUD flow
// the UI depends on.
//
// Every step goes over real HTTP (net/http client against a
// 127.0.0.1:0 listener) so this also guards the JSON-on-the-wire
// contract between the UI and the daemon.
func TestHTTPAPIAddListDeleteCycle(t *testing.T) {
	t.Parallel()

	cfg := baseConfigForRestore(t.TempDir())

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	d, err := daemon.New(context.Background(), daemon.Options{
		Cfg:     cfg,
		Log:     log,
		APIAddr: "127.0.0.1:0",
		Version: "test",
	})
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if d.API == nil {
		t.Fatal("d.API is nil")
	}
	base := "http://" + d.API.Addr()

	client := &http.Client{Timeout: 3 * time.Second}

	// ------------------------------------------------------------
	// 0. List starts empty.
	startList := getTorrentList(t, client, base)
	if len(startList) != 0 {
		t.Fatalf("initial torrent list = %d, want 0", len(startList))
	}

	// ------------------------------------------------------------
	// 1. POST /torrent with a syntactically valid magnet URI. The
	//    magnet points at an infohash no peer will ever have, but
	//    the add path only parses the URI and registers a handle —
	//    the metadata fetch happens asynchronously and we never
	//    wait for it here.
	var ih [20]byte
	for i := range ih {
		ih[i] = byte(i*7 + 3) // deterministic, non-zero
	}
	wantIHHex := metainfo.Hash(ih).HexString()
	magnetURI := metainfo.Magnet{InfoHash: metainfo.Hash(ih), DisplayName: "http-cycle-scenario"}.String()

	addReq := strings.NewReader(`{"uri":` + jsonQuote(magnetURI) + `}`)
	resp, err := client.Post(base+"/torrent", "application/json", addReq)
	if err != nil {
		t.Fatalf("POST /torrent: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /torrent status=%d body=%s", resp.StatusCode, body)
	}
	var addResp struct {
		OK       bool   `json:"ok"`
		InfoHash string `json:"infohash"`
	}
	if err := json.Unmarshal(body, &addResp); err != nil {
		t.Fatalf("unmarshal add response: %v body=%s", err, body)
	}
	if !addResp.OK {
		t.Errorf("add response OK = false")
	}
	if !equalFoldHex(addResp.InfoHash, wantIHHex) {
		t.Errorf("add response infohash = %q, want %q", addResp.InfoHash, wantIHHex)
	}

	// ------------------------------------------------------------
	// 2. GET /torrents must now list it. Poll briefly because the
	//    handle registration is synchronous but the snapshot
	//    builder reads through a few locks.
	var afterAdd []map[string]any
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		afterAdd = getTorrentList(t, client, base)
		if containsInfoHash(afterAdd, wantIHHex) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !containsInfoHash(afterAdd, wantIHHex) {
		t.Fatalf("GET /torrents after add does not contain %s; list=%+v", wantIHHex, afterAdd)
	}

	// ------------------------------------------------------------
	// 3. POST /torrent with the SAME magnet URI should succeed (the
	//    add path dedupes inside anacrolix) and the list must not
	//    grow.
	dupReq := strings.NewReader(`{"uri":` + jsonQuote(magnetURI) + `}`)
	dupResp, err := client.Post(base+"/torrent", "application/json", dupReq)
	if err != nil {
		t.Fatalf("duplicate POST /torrent: %v", err)
	}
	dupBody, _ := io.ReadAll(dupResp.Body)
	dupResp.Body.Close()
	if dupResp.StatusCode != http.StatusOK {
		t.Fatalf("duplicate POST /torrent status=%d body=%s", dupResp.StatusCode, dupBody)
	}
	afterDup := getTorrentList(t, client, base)
	if len(afterDup) != len(afterAdd) {
		t.Errorf("duplicate add changed list size: before=%d after=%d", len(afterAdd), len(afterDup))
	}

	// ------------------------------------------------------------
	// 4. POST /torrent with a malformed body must fail with 400
	//    without affecting the torrent list.
	badResp, err := client.Post(base+"/torrent", "application/json", strings.NewReader(`not json`))
	if err != nil {
		t.Fatalf("malformed POST /torrent: %v", err)
	}
	badResp.Body.Close()
	if badResp.StatusCode != http.StatusBadRequest {
		t.Errorf("malformed POST /torrent status = %d, want 400", badResp.StatusCode)
	}

	// ------------------------------------------------------------
	// 5. POST /torrent with a zero-infohash magnet must fail with
	//    400. This guards the original daemon-teardown bug fix —
	//    an unfiltered panic here would crash the server and take
	//    subsequent requests offline.
	zeroMagnet := "magnet:?xt=urn:btih:0000000000000000000000000000000000000000"
	zeroReq := strings.NewReader(`{"uri":` + jsonQuote(zeroMagnet) + `}`)
	zeroResp, err := client.Post(base+"/torrent", "application/json", zeroReq)
	if err != nil {
		t.Fatalf("zero-infohash POST /torrent: %v", err)
	}
	zeroResp.Body.Close()
	if zeroResp.StatusCode != http.StatusBadRequest {
		t.Errorf("zero-infohash POST /torrent status = %d, want 400", zeroResp.StatusCode)
	}
	// After rejecting a bad add the API must still answer.
	hzResp, err := client.Get(base + "/healthz")
	if err != nil {
		t.Fatalf("healthz after bad add: %v", err)
	}
	hzResp.Body.Close()
	if hzResp.StatusCode != http.StatusOK {
		t.Errorf("healthz after bad add status = %d, want 200", hzResp.StatusCode)
	}

	// ------------------------------------------------------------
	// 6. DELETE /torrents/{infohash} must drop the entry.
	delReq, err := http.NewRequest(http.MethodDelete, base+"/torrents/"+wantIHHex, nil)
	if err != nil {
		t.Fatalf("build DELETE: %v", err)
	}
	delResp, err := client.Do(delReq)
	if err != nil {
		t.Fatalf("DELETE /torrents: %v", err)
	}
	delBody, _ := io.ReadAll(delResp.Body)
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE status=%d body=%s", delResp.StatusCode, delBody)
	}

	// Poll briefly for the remove to propagate through the control
	// adapter.
	var afterDel []map[string]any
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		afterDel = getTorrentList(t, client, base)
		if !containsInfoHash(afterDel, wantIHHex) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if containsInfoHash(afterDel, wantIHHex) {
		t.Fatalf("GET /torrents after delete still contains %s; list=%+v", wantIHHex, afterDel)
	}
}

// getTorrentList fetches GET /torrents and decodes the raw
// torrents array as []map[string]any so the test can probe
// individual fields without pulling in the httpapi package's
// full snapshot struct.
func getTorrentList(t *testing.T, client *http.Client, base string) []map[string]any {
	t.Helper()
	resp, err := client.Get(base + "/torrents")
	if err != nil {
		t.Fatalf("GET /torrents: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /torrents status=%d body=%s", resp.StatusCode, body)
	}
	var payload struct {
		Torrents []map[string]any `json:"torrents"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal /torrents: %v body=%s", err, body)
	}
	return payload.Torrents
}

// containsInfoHash reports whether any torrent in list has
// InfoHash == want (case-insensitive).
func containsInfoHash(list []map[string]any, want string) bool {
	for _, t := range list {
		ih, _ := t["infohash"].(string)
		if equalFoldHex(ih, want) {
			return true
		}
	}
	return false
}

// jsonQuote returns s as a JSON-quoted string literal. The
// standard library's json.Marshal would work too but this is
// shorter and easier to drop into an io.Reader literal.
func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// compile-time: jsonQuote must return a bytes.Buffer-friendly
// value. Keep the Buffer import alive for future test additions.
var _ = bytes.NewReader
