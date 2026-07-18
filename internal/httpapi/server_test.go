package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingHandler captures slog records for assertion.
type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}
func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

func (h *recordingHandler) messages() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.records))
	for i, r := range h.records {
		out[i] = r.Message
	}
	return out
}

func startTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	s := NewWithOptions("localhost:0", slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Version: "vTEST"})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = s.Stop(ctx)
	})
	return s, s.Addr()
}

func TestAddrBeforeStart(t *testing.T) {
	s := NewWithOptions("localhost:0", nil, Options{})
	if got := s.Addr(); got != "" {
		t.Fatalf("Addr() before Start = %q, want empty", got)
	}
}

func TestEmptyAddrDefaultsToCanonical(t *testing.T) {
	s := NewWithOptions("", nil, Options{})
	if s.addr != "localhost:7654" {
		t.Fatalf("empty addr defaulted to %q, want localhost:7654", s.addr)
	}
}

func TestStatusGoldenJSON(t *testing.T) {
	_, addr := startTestServer(t)
	resp, err := http.Get("http://" + addr + "/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	const golden = `{"local":{"indexed":false,"doc_count":0},"swarm":{"known_peers":0,"capable_peers":0},"publisher":{"total_keywords":0,"total_hits":0}}`
	if strings.TrimSpace(string(body)) != golden {
		t.Fatalf("body = %s\nwant  %s", body, golden)
	}
}

// TestStatusWithPubkeyGolden freezes the wired-probe /status body: pubkey
// renders first in the publisher block, independent of any publisher
// collaborator (the legacy nested it under an active publisher — a defect).
func TestStatusWithPubkeyGolden(t *testing.T) {
	pk := strings.Repeat("ab", 32)
	s := NewWithOptions("localhost:0", slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		PublisherPubKey: func() string { return pk },
	})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Stop(context.Background()) }()
	resp, err := http.Get("http://" + s.Addr() + "/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	golden := `{"local":{"indexed":false,"doc_count":0},"swarm":{"known_peers":0,"capable_peers":0},"publisher":{"pubkey":"` + pk + `","total_keywords":0,"total_hits":0}}`
	if strings.TrimSpace(string(body)) != golden {
		t.Fatalf("body = %s\nwant  %s", body, golden)
	}
}

func TestStatusMethodScoped(t *testing.T) {
	_, addr := startTestServer(t)
	req, _ := http.NewRequest("POST", "http://"+addr+"/status", nil)
	req.Header.Set("Origin", "http://localhost:7654")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST /status with clean origin = %d, want 405 (guard passed, mux rejected)", resp.StatusCode)
	}
}

func TestSpoofedOriginRejectedOverWire(t *testing.T) {
	_, addr := startTestServer(t)
	req, _ := http.NewRequest("POST", "http://"+addr+"/status", nil)
	req.Header.Set("Origin", "http://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST with evil origin = %d, want 403", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.TrimSpace(string(body)) != "forbidden: cross-origin request" {
		t.Fatalf("body = %q", body)
	}
}

func TestHealthz(t *testing.T) {
	_, addr := startTestServer(t)
	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var h struct {
		OK      bool   `json:"ok"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		t.Fatal(err)
	}
	if !h.OK || h.Version != "vTEST" {
		t.Fatalf("healthz = %+v", h)
	}
}

func TestHealthzOmitsEmptyVersion(t *testing.T) {
	s := NewWithOptions("localhost:0", slog.New(slog.NewTextHandler(io.Discard, nil)), Options{})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = s.Stop(ctx)
	}()
	resp, err := http.Get("http://" + s.Addr() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "version") {
		t.Fatalf("empty version must be omitted: %s", body)
	}
}

func TestWebStubAndUnknownPaths(t *testing.T) {
	_, addr := startTestServer(t)

	resp, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "SwartzNet") {
		t.Fatalf("GET / = %d, body %q", resp.StatusCode, body)
	}

	resp, err = http.Get("http://" + addr + "/static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET /static/style.css = %d", resp.StatusCode)
	}

	// No SPA fallback: unknown paths 404.
	resp, err = http.Get("http://" + addr + "/nope")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("GET /nope = %d, want 404", resp.StatusCode)
	}
}

func TestStopIdempotentAndRestartable(t *testing.T) {
	s := NewWithOptions("localhost:0", slog.New(slog.NewTextHandler(io.Discard, nil)), Options{})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("second Stop must be a nil no-op, got %v", err)
	}
	if got := s.Addr(); got != "" {
		t.Fatalf("Addr() after Stop = %q, want empty", got)
	}
	// Restart cycle.
	if err := s.Start(); err != nil {
		t.Fatalf("restart failed: %v", err)
	}
	defer func() { _ = s.Stop(context.Background()) }()
	resp, err := http.Get("http://" + s.Addr() + "/status")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("post-restart status = %d", resp.StatusCode)
	}
}

func TestDoubleStartFails(t *testing.T) {
	s := NewWithOptions("localhost:0", slog.New(slog.NewTextHandler(io.Discard, nil)), Options{})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Stop(context.Background()) }()
	first := s.Addr()
	if err := s.Start(); err == nil {
		t.Fatal("second Start without Stop must fail (it would orphan the first listener)")
	}
	if got := s.Addr(); got != first {
		t.Fatalf("failed re-Start changed Addr from %q to %q", first, got)
	}
	resp, err := http.Get("http://" + first + "/status")
	if err != nil {
		t.Fatalf("original listener broken by failed re-Start: %v", err)
	}
	resp.Body.Close()
}

func TestNonLoopbackBindWarnsOnce(t *testing.T) {
	rec := &recordingHandler{}
	s := NewWithOptions("0.0.0.0:0", slog.New(rec), Options{})
	if err := s.Start(); err != nil {
		t.Skipf("cannot bind 0.0.0.0 in this environment: %v", err)
	}
	defer func() { _ = s.Stop(context.Background()) }()

	// Requests must not repeat the warning.
	for i := 0; i < 2; i++ {
		if resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%s/status", portOf(t, s.Addr()))); err == nil {
			resp.Body.Close()
		}
	}
	var warns int
	for _, m := range rec.messages() {
		if m == "httpapi.non_loopback_bind" {
			warns++
		}
	}
	if warns != 1 {
		t.Fatalf("non-loopback warn count = %d, want exactly 1", warns)
	}
	// The load-bearing phrase from the DoD.
	found := false
	rec.mu.Lock()
	for _, r := range rec.records {
		r.Attrs(func(a slog.Attr) bool {
			if strings.Contains(a.Value.String(), "API is UNAUTHENTICATED") {
				found = true
			}
			return true
		})
	}
	rec.mu.Unlock()
	if !found {
		t.Fatal("warning must contain 'API is UNAUTHENTICATED'")
	}
}

func TestLoopbackBindDoesNotWarn(t *testing.T) {
	rec := &recordingHandler{}
	s := NewWithOptions("localhost:0", slog.New(rec), Options{})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Stop(context.Background()) }()
	for _, m := range rec.messages() {
		if m == "httpapi.non_loopback_bind" {
			t.Fatal("loopback bind must not warn")
		}
	}
}

func TestHardeningKnobs(t *testing.T) {
	srv := newHTTPServer(http.NewServeMux())
	if srv.ReadHeaderTimeout != 2*time.Second ||
		srv.ReadTimeout != 5*time.Second ||
		srv.WriteTimeout != 30*time.Second ||
		srv.IdleTimeout != 60*time.Second ||
		srv.MaxHeaderBytes != 1<<18 {
		t.Fatalf("hardening knobs drifted: %+v", srv)
	}
}

func portOf(t *testing.T, addr string) string {
	t.Helper()
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		t.Fatalf("no port in %q", addr)
	}
	return addr[i+1:]
}
