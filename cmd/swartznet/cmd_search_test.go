package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearchUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := cmdSearch(nil, &stdout, &stderr); code != exitUsage {
		t.Fatalf("no args: exit = %d", code)
	}
	if !strings.Contains(stderr.String(), "usage: swartznet search") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestStripMarks(t *testing.T) {
	if got := stripMarks("a <mark>b</mark> c"); got != "a b c" {
		t.Fatalf("stripMarks = %q", got)
	}
}

func TestSearchViaAPIRendersHits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"local":{"total":1,"hits":[{"doc_type":"torrent","infohash":"` + strings.Repeat("ab", 20) + `","name":"Some Book","size_bytes":1024,"score":1.5}]}}`))
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	var stdout, stderr bytes.Buffer
	// --signed-by forces the API path.
	code := cmdSearch([]string{"--api-addr", addr, "--signed-by", strings.Repeat("cd", 32), "book"}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"Query: book", "Local: 1 hits", "=== LOCAL ===", "[torrent] Some Book"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestSearchViaAPIUnreachable(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cmdSearch([]string{"--api-addr", "127.0.0.1:1", "--swarm", "book"}, &stdout, &stderr)
	if code != exitRuntime {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "swartznet: cannot reach the daemon at 127.0.0.1:1") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestSearchViaAPIJSONPassthrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"local":{"total":0,"hits":[]}}`))
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	var stdout, stderr bytes.Buffer
	if code := cmdSearch([]string{"--api-addr", addr, "--dht", "--json", "x"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("not valid json: %v\n%s", err, stdout.String())
	}
}

func TestIndexToggleUsageAndValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"bad arity", []string{"onlyone"}, "swartznet index"},
		{"bad infohash", []string{"nothex", "on"}, "infohash must be 40 hex characters"},
		{"bad mode", []string{strings.Repeat("ab", 20), "maybe"}, "mode must be 'on' or 'off'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := cmdIndex(tc.args, &stdout, &stderr); code != exitUsage {
				t.Fatalf("exit = %d, want 2", code)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestIndexToggleSuccess(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := new(bytes.Buffer)
		_, _ = b.ReadFrom(r.Body)
		gotBody = b.String()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	ih := strings.Repeat("ab", 20)
	var stdout, stderr bytes.Buffer
	if code := cmdIndex([]string{"--api-addr", addr, ih, "off"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(gotBody, `"enabled":false`) {
		t.Fatalf("request body = %q", gotBody)
	}
	if !strings.Contains(stdout.String(), "indexing off: "+ih) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestIndexStatsRendered(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"dir_bytes":2048,"doc_count":5,"torrent_count":2,"content_count":3,"corpus_text_bytes":1000,"inflation_ratio":2.05}`))
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	var stdout, stderr bytes.Buffer
	if code := cmdIndex([]string{"--api-addr", addr}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	for _, want := range []string{"documents:      5", "2 torrents, 3 content", "inflation:      2.05x"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout lacks %q:\n%s", want, stdout.String())
		}
	}
}
