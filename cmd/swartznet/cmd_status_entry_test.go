package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCmdStatusBadFlag covers cmdStatus's
// `if err := fs.Parse(args); err != nil { return exitUsage }` arm.
func TestCmdStatusBadFlag(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdStatus([]string{"--no-such-flag"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("bad-flag exit = %d, want exitUsage", code)
	}
}

// TestCmdStatusBadAPIAddr covers cmdStatus's
// `http.NewRequestWithContext err → reportRunErr` arm at
// cmd_status.go:36-39. An invalid percent-escape in --api-addr
// makes url.Parse reject the URL.
func TestCmdStatusBadAPIAddr(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdStatus([]string{"--api-addr", "%ZZ"}, &stdout, &stderr)
	if code == exitOK {
		t.Errorf("bad-addr exit = %d, want non-zero", code)
	}
}

// TestCmdStatusCannotReachDaemon covers the
// `resp, err := http.DefaultClient.Do(req); if err != nil { … return exitRuntime }`
// arm. Point at a port nothing's listening on.
func TestCmdStatusCannotReachDaemon(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdStatus([]string{"--api-addr", "127.0.0.1:1"}, &stdout, &stderr)
	if code != exitRuntime {
		t.Errorf("unreachable-daemon exit = %d, want exitRuntime", code)
	}
	if !strings.Contains(stderr.String(), "cannot reach the daemon") {
		t.Errorf("expected 'cannot reach the daemon' hint, got %q", stderr.String())
	}
}

// TestCmdStatusNon200 covers the
// `if resp.StatusCode != http.StatusOK { … return exitRuntime }` arm.
func TestCmdStatusNon200(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "kaboom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	var stdout, stderr bytes.Buffer
	code := cmdStatus([]string{"--api-addr", addr}, &stdout, &stderr)
	if code != exitRuntime {
		t.Errorf("non-200 exit = %d, want exitRuntime", code)
	}
}

// TestCmdStatusBadJSONBody covers the
// `if err := json.NewDecoder(resp.Body).Decode(&out); err != nil { … }` arm.
func TestCmdStatusBadJSONBody(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/status") {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "this is not json {")
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	var stdout, stderr bytes.Buffer
	code := cmdStatus([]string{"--api-addr", addr}, &stdout, &stderr)
	if code == exitOK {
		t.Errorf("bad-json exit = %d, want non-zero (Decode err)", code)
	}
}

// TestCmdStatusHappyPathText covers the success path: 200 OK
// with valid JSON status body, then emitStatusText runs and the
// output contains expected fields.
func TestCmdStatusHappyPathText(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/status") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"local":{"indexed":true,"docs":4},"swarm":{"known_peers":2,"capable_peers":1}}`)
			return
		}
		// /aggregate → 404, treated as best-effort skip.
		http.NotFound(w, r)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	var stdout, stderr bytes.Buffer
	code := cmdStatus([]string{"--api-addr", addr}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("happy-path exit = %d, stderr: %s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "SwartzNet daemon status") {
		t.Errorf("missing header in output: %s", out)
	}
}

// TestCmdStatusHappyPathJSON covers the --json branch.
func TestCmdStatusHappyPathJSON(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/status") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"local":{"indexed":true,"docs":4}}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	var stdout, stderr bytes.Buffer
	code := cmdStatus([]string{"--api-addr", addr, "--json"}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("--json exit = %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"status"`) {
		t.Errorf("expected JSON 'status' key, got %s", stdout.String())
	}
}
