package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCmdFlagBadFlag covers cmdFlagOrConfirm's `fs.Parse` err arm
// (via cmdFlag).
func TestCmdFlagBadFlag(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdFlag([]string{"--no-such-flag"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("bad-flag exit = %d, want exitUsage", code)
	}
}

// TestCmdFlagNoArgs covers the `if fs.NArg() != 1` arm.
func TestCmdFlagNoArgs(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdFlag(nil, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("no-args exit = %d, want exitUsage", code)
	}
}

// TestCmdFlagBadInfohash covers the `len(infoHash) != 40` arm.
func TestCmdFlagBadInfohash(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdFlag([]string{"too-short"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("bad-ih exit = %d, want exitUsage", code)
	}
}

// TestCmdFlagUnreachableDaemon covers the
// `Do err → cannot reach the daemon` arm.
func TestCmdFlagUnreachableDaemon(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdFlag([]string{"--api-addr", "127.0.0.1:1", validIH}, &stdout, &stderr)
	if code != exitRuntime {
		t.Errorf("unreachable exit = %d, want exitRuntime", code)
	}
}

// TestCmdFlagNon200 covers the `resp.StatusCode != 200` arm.
func TestCmdFlagNon200(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusInternalServerError)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	var stdout, stderr bytes.Buffer
	code := cmdFlag([]string{"--api-addr", addr, validIH}, &stdout, &stderr)
	if code != exitRuntime {
		t.Errorf("non-200 exit = %d, want exitRuntime", code)
	}
}

// TestCmdFlagHappyPath covers the `if out.OK { … "flagged" } return exitOK`
// success path. Server returns OK=true; output prints "flagged: <ih>".
func TestCmdFlagHappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"ok":true,"infohash":"%s"}`, validIH)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	var stdout, stderr bytes.Buffer
	code := cmdFlag([]string{"--api-addr", addr, validIH}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("happy exit = %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "flagged: "+validIH) {
		t.Errorf("expected 'flagged: %s' in output, got %q", validIH, stdout.String())
	}
}

// TestCmdFlagNotOK covers the `if !out.OK { … return exitRuntime }`
// arm — server returns OK=false.
func TestCmdFlagNotOK(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok":false}`)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	var stdout, stderr bytes.Buffer
	code := cmdFlag([]string{"--api-addr", addr, validIH}, &stdout, &stderr)
	if code != exitRuntime {
		t.Errorf("not-OK exit = %d, want exitRuntime", code)
	}
}

// TestCmdFlagBadJSONBody covers the
// `json.NewDecoder(resp.Body).Decode err → reportRunErr` arm.
// Server returns 200 OK with a non-JSON body.
func TestCmdFlagBadJSONBody(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "this is not json {")
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	var stdout, stderr bytes.Buffer
	code := cmdFlag([]string{"--api-addr", addr, validIH}, &stdout, &stderr)
	if code == exitOK {
		t.Errorf("bad-json exit = %d, want non-zero", code)
	}
}

// TestCmdConfirmHappyPath covers cmdConfirm's success path —
// uses the same cmdFlagOrConfirm helper so 'confirmed' past
// tense is the asserted difference.
func TestCmdConfirmHappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"ok":true,"infohash":"%s"}`, validIH)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	var stdout, stderr bytes.Buffer
	code := cmdConfirm([]string{"--api-addr", addr, validIH}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("confirm exit = %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "confirmed: "+validIH) {
		t.Errorf("expected 'confirmed: %s' in output, got %q", validIH, stdout.String())
	}
}
