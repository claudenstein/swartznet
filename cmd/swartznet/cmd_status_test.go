package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const degradedStatusJSON = `{"local":{"indexed":false,"doc_count":0},"swarm":{"known_peers":0,"capable_peers":0},"publisher":{"total_keywords":0,"total_hits":0}}`

func statusTestServer(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func TestStatusUnreachable(t *testing.T) {
	var stdout, stderr bytes.Buffer
	// Port 1 on loopback refuses fast.
	code := cmdStatus([]string{"--api-addr", "127.0.0.1:1"}, &stdout, &stderr)
	if code != exitRuntime {
		t.Fatalf("exit = %d, want 1", code)
	}
	errOut := stderr.String()
	if !strings.Contains(errOut, "swartznet: cannot reach the daemon at 127.0.0.1:1") {
		t.Fatalf("stderr = %q", errOut)
	}
	if !strings.Contains(errOut, "start it with: swartznet add <magnet>") {
		t.Fatalf("stderr %q lacks the start hint", errOut)
	}
}

func TestStatusNon200(t *testing.T) {
	addr := statusTestServer(t, 500, "boom\n")
	var stdout, stderr bytes.Buffer
	code := cmdStatus([]string{"--api-addr", addr}, &stdout, &stderr)
	if code != exitRuntime {
		t.Fatalf("exit = %d, want 1", code)
	}
	// The body is included verbatim (untrimmed), byte-identical to legacy.
	if got := stderr.String(); got != "swartznet: api status 500: boom\n\n" {
		t.Fatalf("stderr = %q", got)
	}
}

func TestStatusDecodeError(t *testing.T) {
	addr := statusTestServer(t, 200, "not json")
	var stdout, stderr bytes.Buffer
	code := cmdStatus([]string{"--api-addr", addr}, &stdout, &stderr)
	if code != exitRuntime {
		t.Fatalf("exit = %d, want 1", code)
	}
	// The raw decoder error passes through reportRunErr unwrapped (legacy shape).
	if !strings.HasPrefix(stderr.String(), "swartznet: invalid character") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestStatusTextDegraded(t *testing.T) {
	addr := statusTestServer(t, 200, degradedStatusJSON)
	var stdout, stderr bytes.Buffer
	code := cmdStatus([]string{"--api-addr", addr}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	const golden = `SwartzNet daemon status

Local index (Layer L):
  not configured

Swarm search (Layer S, sn_search BEP-10 extension):
  known peers:    0
  capable peers:  0

DHT publisher (Layer D, BEP-44 keyword index):
  total keywords: 0
  total hits:     0
  (no keywords published yet)
`
	if stdout.String() != golden {
		t.Fatalf("text output:\n%q\nwant:\n%q", stdout.String(), golden)
	}
	// The DHT section must be absent when the JSON omits the dht block.
	if strings.Contains(stdout.String(), "DHT routing table") {
		t.Fatal("DHT section rendered for an omitted dht block")
	}
}

func TestStatusJSONEnvelope(t *testing.T) {
	addr := statusTestServer(t, 200, degradedStatusJSON)
	var stdout, stderr bytes.Buffer
	code := cmdStatus([]string{"--api-addr", addr, "--json"}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	out := stdout.String()
	if !strings.HasPrefix(out, "{\n  \"status\": {") {
		t.Fatalf("json output not an indented status envelope:\n%s", out)
	}
	if strings.Contains(out, "\"aggregate\"") {
		t.Fatalf("aggregate block must be omitted:\n%s", out)
	}
	for _, field := range []string{"\"local\"", "\"swarm\"", "\"publisher\"", "\"doc_count\"", "\"known_peers\""} {
		if !strings.Contains(out, field) {
			t.Fatalf("json output lacks %s:\n%s", field, out)
		}
	}
}

func TestStatusRendersKeywordTable(t *testing.T) {
	withKeywords := `{"local":{"indexed":false,"doc_count":0},"swarm":{"known_peers":0,"capable_peers":0},"publisher":{"total_keywords":2,"total_hits":9,"keywords":[{"keyword":"ubuntu","hits_count":7,"publish_count":3,"last_published":"2026-07-17T00:00:00Z"},{"keyword":"iso","hits_count":2,"publish_count":1,"last_error":"put failed"}]}}`
	addr := statusTestServer(t, 200, withKeywords)
	var stdout, stderr bytes.Buffer
	if code := cmdStatus([]string{"--api-addr", addr}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	out := stdout.String()
	for _, want := range []string{
		"  per-keyword:",
		"    ubuntu               hits=7    publishes=3    last=2026-07-17T00:00:00Z      state=ok",
		"    iso                  hits=2    publishes=1    last=never                     state=ERR: put failed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "(no keywords published yet)") {
		t.Fatal("empty-state line rendered alongside keyword table")
	}
}

func TestStatusRendersDHTBlockWhenPresent(t *testing.T) {
	withDHT := `{"local":{"indexed":true,"doc_count":7},"swarm":{"known_peers":1,"capable_peers":2},"publisher":{"pubkey":"abcd","total_keywords":3,"total_hits":4},"dht":{"good_nodes":5,"nodes":6}}`
	addr := statusTestServer(t, 200, withDHT)
	var stdout, stderr bytes.Buffer
	if code := cmdStatus([]string{"--api-addr", addr}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	out := stdout.String()
	for _, want := range []string{
		"enabled, 7 documents",
		"DHT routing table:",
		"good nodes:     5",
		"total nodes:    6",
		"pubkey:         abcd",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
}
