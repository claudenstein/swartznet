package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/httpapi"
	"github.com/swartznet/swartznet/internal/indexer"
)

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestHTTPSearchLocalOnly(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "index.bleve"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	if err := idx.IndexTorrent(indexer.TorrentDoc{
		InfoHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Name:     "Ubuntu 24.04 desktop amd64",
	}); err != nil {
		t.Fatal(err)
	}

	s := httpapi.New("localhost:0", idx, nil, silentLogger())
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Stop(context.Background()) }()

	reqBody, _ := json.Marshal(httpapi.SearchRequest{Q: "ubuntu", Limit: 10})
	resp, err := http.Post("http://"+s.Addr()+"/search", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	var out httpapi.SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Local.Total != 1 {
		t.Errorf("local total = %d, want 1", out.Local.Total)
	}
	if len(out.Local.Hits) != 1 || !strings.HasPrefix(out.Local.Hits[0].InfoHash, "aaaaaaaa") {
		t.Errorf("hits = %+v", out.Local.Hits)
	}
	if out.Swarm != nil {
		t.Errorf("swarm section should be nil when swarm=false, got %+v", out.Swarm)
	}
}

func TestHTTPSearchMissingQueryRejects(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "index.bleve"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	s := httpapi.New("localhost:0", idx, nil, silentLogger())
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Stop(context.Background()) }()

	reqBody, _ := json.Marshal(httpapi.SearchRequest{Q: ""})
	resp, err := http.Post("http://"+s.Addr()+"/search", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", resp.StatusCode)
	}
}

func TestHTTPHealthz(t *testing.T) {
	t.Parallel()
	s := httpapi.New("localhost:0", nil, nil, silentLogger())
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Stop(context.Background()) }()

	resp, err := http.Get("http://" + s.Addr() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"ok":true`) {
		t.Errorf("healthz body = %q", body)
	}
}

func TestHTTPStopIsIdempotent(t *testing.T) {
	t.Parallel()
	s := httpapi.New("localhost:0", nil, nil, silentLogger())
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Errorf("first stop: %v", err)
	}
	if err := s.Stop(ctx); err != nil {
		t.Errorf("second stop: %v", err)
	}
}
