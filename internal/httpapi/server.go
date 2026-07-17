// Package httpapi is the localhost HTTP control plane and embedded web UI.
//
// Architectural law: this package imports no SwartzNet subsystem. Every
// collaborator is a narrow interface declared here; every DTO is declared and
// JSON-tagged here; the daemon wires adapters. A nil collaborator makes its
// handler answer 503. No auth-token or session concept exists anywhere in
// this package — the security model is the loopback-default bind plus the
// CSRF/DNS-rebind guard, by construction.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/swartznet/swartznet/internal/httpapi/web"
)

// Options carries the server's collaborators. It grows slice by slice; a
// zero Options is valid and yields a fully degraded (but working) API.
type Options struct {
	// Version is reported by GET /healthz; empty omits the field.
	Version string
}

// Server is the HTTP API server. It is reusable across Start/Stop cycles.
type Server struct {
	addr string
	log  *slog.Logger
	opts Options

	mu         sync.Mutex
	listener   net.Listener
	httpServer *http.Server
}

// NewWithOptions builds a server bound to addr ("" defaults to
// localhost:7654). A nil log falls back to slog.Default().
func NewWithOptions(addr string, log *slog.Logger, opts Options) *Server {
	if log == nil {
		log = slog.Default()
	}
	if addr == "" {
		addr = "localhost:7654"
	}
	return &Server{addr: addr, log: log, opts: opts}
}

// Addr returns the effective listen address, or "" before Start.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// Start binds synchronously — so a ":0" caller can immediately read Addr() —
// and serves in a background goroutine. A second Start without an intervening
// Stop is an error: succeeding silently would orphan the first listener. A
// non-loopback bind is honored but warned about loudly, once: the API carries
// no authentication.
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return errors.New("httpapi: already started")
	}
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.warnIfNonLoopback(ln.Addr())

	mux := http.NewServeMux()
	s.routes(mux)
	srv := newHTTPServer(withMaxBodyBytes(withCSRFGuard(mux), maxRequestBody))
	s.listener = ln
	s.httpServer = srv

	// srv and ln are passed as parameters so this goroutine never races
	// Stop's field reset.
	go func(srv *http.Server, ln net.Listener) {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log.Warn("httpapi.serve_err", "err", err)
		}
	}(srv, ln)

	s.log.Info("httpapi.listening", "addr", ln.Addr().String())
	return nil
}

// newHTTPServer applies the hardening knobs shared by every Start.
func newHTTPServer(h http.Handler) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 18,
	}
}

func (s *Server) warnIfNonLoopback(addr net.Addr) {
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil || host == "" {
		return
	}
	if !isLoopbackHostHeader(host) {
		s.log.Warn("httpapi.non_loopback_bind",
			"addr", addr.String(),
			"msg", "API is UNAUTHENTICATED and reachable off-host; anyone who can reach this address controls torrents, capabilities, and reputation")
	}
}

// Stop shuts the server down gracefully. It is idempotent; after Stop the
// server can Start again.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	srv := s.httpServer
	s.httpServer = nil
	s.listener = nil
	s.mu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}

// routes registers the API endpoints, then the embedded web UI. API routes
// register first so they always win over the static tree.
func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /status", s.handleStatus)

	if assetsFS, err := fs.Sub(web.Assets(), "."); err == nil {
		mux.Handle("GET /static/", http.FileServer(http.FS(assetsFS)))
		// "GET /{$}" matches only the exact root: unknown paths 404, there is
		// deliberately no SPA index fallback.
		mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
			http.ServeFileFS(w, r, assetsFS, "index.html")
		})
	} else {
		s.log.Warn("httpapi.web_assets_unavailable", "err", err)
	}
}

// handleStatus renders the node status. It never fails and never 503s: each
// block renders its honest degraded state while a subsystem is unwired.
func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	var out StatusResponse
	// No collaborators exist yet at this slice; every optional block stays
	// nil and the value blocks render zero state.
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(healthzResponse{OK: true, Version: s.opts.Version})
}
