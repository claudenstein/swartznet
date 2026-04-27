package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCmdIndexBadFlag covers cmdIndex's `fs.Parse` err arm.
func TestCmdIndexBadFlag(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdIndex([]string{"--no-such-flag"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("bad-flag exit = %d, want exitUsage", code)
	}
}

// TestCmdIndexBadArgs covers `if fs.NArg() != 2` arm.
func TestCmdIndexBadArgs(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdIndex([]string{"only-one"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("bad-args exit = %d, want exitUsage", code)
	}
}

// TestCmdIndexBadInfohash covers `if len(ih) != 40` arm.
func TestCmdIndexBadInfohash(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdIndex([]string{"too-short", "on"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("bad-ih exit = %d, want exitUsage", code)
	}
}

// TestCmdIndexBadMode covers the `default: → exitUsage` arm of
// the mode switch.
func TestCmdIndexBadMode(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdIndex([]string{validIH, "maybe"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("bad-mode exit = %d, want exitUsage", code)
	}
}

// TestCmdIndexBadAPIAddr covers cmdIndex's
// `http.NewRequestWithContext err → reportRunErr` arm. Invalid
// percent-escape in --api-addr trips url.Parse.
func TestCmdIndexBadAPIAddr(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdIndex([]string{"--api-addr", "%ZZ", validIH, "on"}, &stdout, &stderr)
	if code == exitOK {
		t.Errorf("bad-addr exit = %d, want non-zero", code)
	}
}

// TestCmdIndexUnreachableDaemon covers the
// `Do err → cannot reach the daemon` arm.
func TestCmdIndexUnreachableDaemon(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdIndex([]string{
		"--api-addr", "127.0.0.1:1",
		validIH, "on",
	}, &stdout, &stderr)
	if code != exitRuntime {
		t.Errorf("unreachable exit = %d, want exitRuntime", code)
	}
}

// TestCmdIndexNon200 covers the non-200 arm.
func TestCmdIndexNon200(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "kaboom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	var stdout, stderr bytes.Buffer
	code := cmdIndex([]string{"--api-addr", addr, validIH, "on"}, &stdout, &stderr)
	if code != exitRuntime {
		t.Errorf("non-200 exit = %d, want exitRuntime", code)
	}
}

// TestCmdIndexHappyOn covers the happy path with mode=on:
// stdout prints "indexing on: <ih>".
func TestCmdIndexHappyOn(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	var stdout, stderr bytes.Buffer
	code := cmdIndex([]string{"--api-addr", addr, validIH, "on"}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("happy-on exit = %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "indexing on: "+validIH) {
		t.Errorf("expected 'indexing on: %s' in output, got %q", validIH, stdout.String())
	}
}

// TestCmdIndexHappyOff covers the !enabled side: "off"
// formatting.
func TestCmdIndexHappyOff(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	var stdout, stderr bytes.Buffer
	code := cmdIndex([]string{"--api-addr", addr, validIH, "off"}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("happy-off exit = %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "indexing off: "+validIH) {
		t.Errorf("expected 'indexing off: %s' in output, got %q", validIH, stdout.String())
	}
}
