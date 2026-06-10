package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const validIH = "0123456789abcdef0123456789abcdef01234567"

// TestCmdFilesBadFlag covers cmdFiles's `fs.Parse` err arm.
func TestCmdFilesBadFlag(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdFiles([]string{"--no-such-flag"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("bad-flag exit = %d, want exitUsage", code)
	}
}

// TestCmdFilesNoArgs covers the `default` arm in the `switch fs.NArg()`
// dispatch — zero positional args prints the usage banner.
func TestCmdFilesNoArgs(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdFiles(nil, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("no-args exit = %d, want exitUsage", code)
	}
}

// TestCmdFilesBadInfohash covers filesList's
// `if len(ih) != 40 { return exitUsage }` arm.
func TestCmdFilesBadInfohash(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdFiles([]string{"too-short"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("bad-ih exit = %d, want exitUsage", code)
	}
}

// TestCmdFilesUnreachableDaemon covers filesList's
// `Do err → cannot reach the daemon` arm.
func TestCmdFilesUnreachableDaemon(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdFiles([]string{
		"--api-addr", "127.0.0.1:1",
		validIH,
	}, &stdout, &stderr)
	if code != exitRuntime {
		t.Errorf("unreachable exit = %d, want exitRuntime", code)
	}
}

// TestCmdFilesListBadAPIAddr covers filesList's
// `http.NewRequestWithContext err → reportRunErr` arm. An
// --api-addr containing an invalid percent-escape makes
// url.Parse fail before any network IO is attempted.
func TestCmdFilesListBadAPIAddr(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdFiles([]string{"--api-addr", "%ZZ", validIH}, &stdout, &stderr)
	if code == exitOK {
		t.Errorf("bad-addr list exit = %d, want non-zero", code)
	}
}

// TestCmdFilesSetPriorityBadAPIAddr covers filesSetPriority's
// `http.NewRequestWithContext err` arm via the same trick. The
// 3-positional-arg form (<ih> <index> <priority>) routes
// cmdFiles into filesSetPriority.
func TestCmdFilesSetPriorityBadAPIAddr(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdFiles([]string{
		"--api-addr", "%ZZ",
		validIH, "0", "normal",
	}, &stdout, &stderr)
	if code == exitOK {
		t.Errorf("bad-addr set-priority exit = %d, want non-zero", code)
	}
}

// TestCmdFilesNon200 covers filesList's non-200 arm.
func TestCmdFilesNon200(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "kaboom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	var stdout, stderr bytes.Buffer
	code := cmdFiles([]string{"--api-addr", addr, validIH}, &stdout, &stderr)
	if code != exitRuntime {
		t.Errorf("non-200 exit = %d, want exitRuntime", code)
	}
}

// TestCmdFilesEmptyHappy covers filesList's `len(body.Files)==0`
// branch — daemon returns 200 with no files (pre-metadata torrent).
func TestCmdFilesEmptyHappy(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"files":[]}`)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	var stdout, stderr bytes.Buffer
	code := cmdFiles([]string{"--api-addr", addr, validIH}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("empty-files exit = %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "no files") {
		t.Errorf("expected 'no files' message, got %q", stdout.String())
	}
}

// TestCmdFilesHappyTable covers filesList's table-rendering path.
func TestCmdFilesHappyTable(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"files":[{"index":0,"priority":"normal","length":2048,"progress":0.5,"display_path":"a/b.txt"}]}`)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	var stdout, stderr bytes.Buffer
	code := cmdFiles([]string{"--api-addr", addr, validIH}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("happy exit = %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "a/b.txt") {
		t.Errorf("expected 'a/b.txt' in output, got %q", stdout.String())
	}
}

// TestCmdFilesHappyJSON covers filesList's --json branch.
func TestCmdFilesHappyJSON(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"files":[{"index":0,"priority":"high"}]}`)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	var stdout, stderr bytes.Buffer
	code := cmdFiles([]string{"--api-addr", addr, "--json", validIH}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("--json exit = %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"index"`) {
		t.Errorf("expected JSON 'index' field, got %q", stdout.String())
	}
}

// TestCmdFilesSetPriorityBadInfohash covers filesSetPriority's
// `len(ih) != 40 → exitUsage` arm via the 3-arg branch.
func TestCmdFilesSetPriorityBadInfohash(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdFiles([]string{"too-short", "0", "high"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("bad-ih exit = %d, want exitUsage", code)
	}
}

// TestCmdFilesSetPriorityBadPriority covers the
// `switch prio { case "none", "normal", "high": ; default: return exitUsage }`
// arm.
func TestCmdFilesSetPriorityBadPriority(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdFiles([]string{validIH, "0", "weird"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("bad-priority exit = %d, want exitUsage", code)
	}
}

// TestCmdFilesSetPriorityBadIndex covers filesSetPriority's index
// validation: anything that is not a plain non-negative integer
// must be rejected before it is interpolated into the URL path.
func TestCmdFilesSetPriorityBadIndex(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		idx  string
	}{
		{name: "empty", idx: ""},
		{name: "alpha", idx: "abc"},
		{name: "trailing-garbage", idx: "0abc"},
		{name: "negative", idx: "-1"},
		{name: "float", idx: "1.5"},
		{name: "path-segment", idx: "../0"},
		{name: "embedded-space", idx: "1 2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := cmdFiles([]string{validIH, tc.idx, "high"}, &stdout, &stderr)
			if code != exitUsage {
				t.Errorf("idx %q exit = %d, want exitUsage", tc.idx, code)
			}
			if !strings.Contains(stderr.String(), "non-negative integer") {
				t.Errorf("idx %q: expected index error in stderr, got %q", tc.idx, stderr.String())
			}
		})
	}
}

// TestCmdFilesSetPriorityIndexCanonicalized verifies the index is
// sent in canonical decimal form — "+07" parses but must reach the
// daemon as "7", never verbatim.
func TestCmdFilesSetPriorityIndexCanonicalized(t *testing.T) {
	t.Parallel()
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	var stdout, stderr bytes.Buffer
	code := cmdFiles([]string{
		"--api-addr", addr,
		validIH, "+07", "normal",
	}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("canonical-index exit = %d, stderr: %s", code, stderr.String())
	}
	want := "/torrents/" + validIH + "/files/7/priority"
	if gotPath != want {
		t.Errorf("request path = %q, want %q", gotPath, want)
	}
}

// TestCmdFilesSetPriorityUnreachable covers filesSetPriority's
// `Do err → cannot reach the daemon` arm via the 3-arg branch.
func TestCmdFilesSetPriorityUnreachable(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdFiles([]string{
		"--api-addr", "127.0.0.1:1",
		validIH, "0", "high",
	}, &stdout, &stderr)
	if code != exitRuntime {
		t.Errorf("unreachable exit = %d, want exitRuntime", code)
	}
}

// TestCmdFilesSetPriorityNon200 covers the non-200 arm of
// filesSetPriority.
func TestCmdFilesSetPriorityNon200(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "kaboom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	var stdout, stderr bytes.Buffer
	code := cmdFiles([]string{
		"--api-addr", addr,
		validIH, "0", "normal",
	}, &stdout, &stderr)
	if code != exitRuntime {
		t.Errorf("non-200 exit = %d, want exitRuntime", code)
	}
}

// TestCmdFilesListBadJSON covers filesList's
// `json.NewDecoder(resp.Body).Decode err → reportRunErr` arm.
func TestCmdFilesListBadJSON(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "this is not json {")
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	var stdout, stderr bytes.Buffer
	code := cmdFiles([]string{"--api-addr", addr, validIH}, &stdout, &stderr)
	if code == exitOK {
		t.Errorf("bad-json exit = %d, want non-zero", code)
	}
}

// TestCmdFilesSetPriorityHappy covers filesSetPriority's success
// path: server returns 200 → "file 0 of <ih>: priority=high".
func TestCmdFilesSetPriorityHappy(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	var stdout, stderr bytes.Buffer
	code := cmdFiles([]string{
		"--api-addr", addr,
		validIH, "0", "high",
	}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("happy exit = %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "priority=high") {
		t.Errorf("expected 'priority=high' in stdout, got %q", stdout.String())
	}
}
