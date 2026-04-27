package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/httpapi"
)

// TestCmdSearchBadFlag covers cmdSearch's `fs.Parse` err arm.
func TestCmdSearchBadFlag(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdSearch([]string{"--no-such-flag"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("bad-flag exit = %d, want exitUsage", code)
	}
}

// TestCmdSearchNoArgs covers the `if fs.NArg() == 0` arm.
func TestCmdSearchNoArgs(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdSearch(nil, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("no-args exit = %d, want exitUsage", code)
	}
}

// TestCmdSearchSwarmUnreachable covers cmdSearchViaAPI's
// `Do err → cannot reach the daemon` arm via --swarm.
func TestCmdSearchSwarmUnreachable(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdSearch([]string{
		"--swarm",
		"--api-addr", "127.0.0.1:1",
		"ubuntu",
	}, &stdout, &stderr)
	if code != exitRuntime {
		t.Errorf("unreachable exit = %d, want exitRuntime", code)
	}
}

// TestCmdSearchSwarmNon200 covers cmdSearchViaAPI's non-200 arm.
func TestCmdSearchSwarmNon200(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "kaboom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	var stdout, stderr bytes.Buffer
	code := cmdSearch([]string{
		"--swarm",
		"--api-addr", addr,
		"ubuntu",
	}, &stdout, &stderr)
	if code != exitRuntime {
		t.Errorf("non-200 exit = %d, want exitRuntime", code)
	}
}

// TestCmdSearchSwarmHappyText covers the happy text path through
// cmdSearchViaAPI + emitSwarmText.
func TestCmdSearchSwarmHappyText(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"query":"ubuntu",
			"local":{"total":1,"hits":[{"infohash":"a","name":"ubuntu.iso","size_bytes":2048,"score":0.9,"doc_type":"torrent"}]},
			"swarm":{"asked":2,"responded":1,"rejected":0,"hits":[{"infohash":"b","name":"ubuntu-swarm","size":4096,"seeders":3,"score":7,"sources":["pk"]}]},
			"dht":{"indexers_asked":1,"indexers_responded":1,"hits":[{"infohash":"c","name":"ubuntu-dht","size":1024,"seeders":2,"files":1,"sources":["pk"]}]}
		}`)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	var stdout, stderr bytes.Buffer
	code := cmdSearch([]string{
		"--swarm", "--dht",
		"--api-addr", addr,
		"ubuntu",
	}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("happy exit = %d, stderr: %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"=== LOCAL ===", "=== SWARM ===", "=== DHT ==="} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

// TestCmdSearchSwarmHappyJSON covers cmdSearchViaAPI's --json branch.
func TestCmdSearchSwarmHappyJSON(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"query":"q","local":{"total":0,"hits":[]}}`)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	var stdout, stderr bytes.Buffer
	code := cmdSearch([]string{
		"--swarm",
		"--json",
		"--api-addr", addr,
		"x",
	}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("happy --json exit = %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"local"`) {
		t.Errorf("expected JSON 'local' field, got %s", stdout.String())
	}
}

// TestEmitSwarmTextNoResults covers emitSwarmText's `(no results)`
// arm — empty Local/Swarm/DHT.
func TestEmitSwarmTextNoResults(t *testing.T) {
	t.Parallel()
	res := &httpapi.SearchResponse{
		Local: httpapi.LocalResult{Total: 0, Hits: nil},
	}
	var buf bytes.Buffer
	if code := emitSwarmText(&buf, res, "q"); code != exitOK {
		t.Fatal(code)
	}
	if !strings.Contains(buf.String(), "(no results)") {
		t.Errorf("expected '(no results)' in output: %s", buf.String())
	}
}

// TestEmitSwarmTextSwarmAndDHTErrors covers emitSwarmText's
// `if res.Swarm.Error != ""` and `if res.DHT.Error != ""` arms.
func TestEmitSwarmTextSwarmAndDHTErrors(t *testing.T) {
	t.Parallel()
	res := &httpapi.SearchResponse{
		Local: httpapi.LocalResult{Total: 0, Hits: nil},
		Swarm: &httpapi.SwarmResult{Asked: 1, Error: "boom-swarm"},
		DHT:   &httpapi.DHTResult{IndexersAsked: 1, Error: "boom-dht"},
	}
	var buf bytes.Buffer
	if code := emitSwarmText(&buf, res, "q"); code != exitOK {
		t.Fatal(code)
	}
	out := buf.String()
	if !strings.Contains(out, "Swarm error: boom-swarm") {
		t.Errorf("missing swarm error line: %s", out)
	}
	if !strings.Contains(out, "DHT error: boom-dht") {
		t.Errorf("missing DHT error line: %s", out)
	}
}

// TestPrintLocalHitContentDoc covers printLocalHit's
// `case "content"` branch.
func TestPrintLocalHitContentDoc(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	printLocalHit(&buf, 1, httpapi.LocalHit{
		DocType:   "content",
		FilePath:  "a/b.txt",
		SizeBytes: 1024,
		Extractor: "plaintext",
		InfoHash:  validIH,
		Score:     0.5,
	})
	out := buf.String()
	if !strings.Contains(out, "[content]") {
		t.Errorf("expected [content] tag in output: %s", out)
	}
	if !strings.Contains(out, "extractor=plaintext") {
		t.Errorf("expected extractor field: %s", out)
	}
}

// TestPrintLocalHitTorrentDoc covers printLocalHit's
// `default` branch (torrent doc).
func TestPrintLocalHitTorrentDoc(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	printLocalHit(&buf, 2, httpapi.LocalHit{
		DocType:   "torrent",
		Name:      "movie.mkv",
		SizeBytes: 4096,
		InfoHash:  validIH,
		Score:     0.7,
	})
	out := buf.String()
	if !strings.Contains(out, "[torrent]") {
		t.Errorf("expected [torrent] tag in output: %s", out)
	}
	if !strings.Contains(out, "movie.mkv") {
		t.Errorf("expected name in output: %s", out)
	}
}

// TestPrintDHTHit + TestPrintSwarmHit cover the small print
// helpers — they're pure functions; just need a smoke run.
func TestPrintDHTHit(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	printDHTHit(&buf, 1, httpapi.DHTHit{
		InfoHash: validIH,
		Name:     "ubuntu-dht.iso",
		Size:     2048,
		Seeders:  3,
		Files:    2,
		Sources:  []string{"pk-a"},
	})
	if !strings.Contains(buf.String(), "ubuntu-dht.iso") {
		t.Errorf("expected name in output: %s", buf.String())
	}
}

func TestPrintSwarmHit(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	printSwarmHit(&buf, 1, httpapi.SwarmHit{
		InfoHash: validIH,
		Name:     "ubuntu-swarm.iso",
		Size:     8192,
		Seeders:  4,
		Score:    7,
		Sources:  []string{"pk-a", "pk-b"},
	})
	if !strings.Contains(buf.String(), "ubuntu-swarm.iso") {
		t.Errorf("expected name in output: %s", buf.String())
	}
}
