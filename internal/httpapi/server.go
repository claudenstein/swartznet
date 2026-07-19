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
	// PublisherPubKey reports the node's publisher public key as 64
	// lowercase hex characters; the daemon wires identity.PublicKeyHex
	// here. Nil-safe: nil (or an empty return) omits the /status field.
	// Deliberately independent of any publisher collaborator — the pubkey
	// renders whenever an identity is loaded.
	PublisherPubKey func() string
	// Adder accepts magnet adds; nil ⇒ POST /torrent answers 503.
	Adder TorrentAdder
	// Control is the torrent control surface; nil ⇒ its endpoints answer
	// 503.
	Control TorrentController
	// DHTStats reports (good, total) routing-table nodes. Nil means the DHT
	// is disabled and the /status dht block is omitted entirely — which is
	// deliberately distinct from a present block with zero nodes.
	DHTStats func() (good, total int)
	// Search runs a search across the wired layers. Nil ⇒ POST /search
	// still answers 200 with an empty local block (Layer L simply off) —
	// the "always run Layer L when wired" contract deliberately overrides
	// the nil⇒503 law for this endpoint. /status local.indexed follows it.
	Search func(SearchParams) SearchResult
	// IndexStats reports Bleve index statistics. Nil ⇒ GET /index/stats
	// answers 503 (the index IS the endpoint's whole purpose).
	IndexStats func() (IndexStats, error)
	// LocalDocCount reports the index document count for /status; nil ⇒
	// local.indexed=false.
	LocalDocCount func() (uint64, error)
	// Confirm / Flag are the ONE shared spam-signal path. Nil ⇒ 503. Both
	// return the httpapi error sentinels for bad-infohash / not-configured.
	Confirm func(infohash string) (ConfirmResult, error)
	Flag    func(infohash string) (FlagResult, error)
	// BloomStat / ReputationStat feed the /status blocks; nil ⇒ block
	// omitted.
	BloomStat      func() *BloomStatus
	ReputationStat func() *ReputationStat
	// Aggregate feeds GET /aggregate; nil ⇒ 503. Its Services field is
	// overwritten from ServicesReporter so the mask has a single render path.
	Aggregate func() AggregateStatusResponse
	// ServicesReporter reports the LIVE 64-bit sn_search services mask,
	// rendered as 16-hex for /capabilities and /aggregate. Nil ⇒ all-zero.
	// This is the single readout of the one mask producer (Slice 6).
	ServicesReporter func() uint64
	// Capabilities is the sn_search sharing-prefs collaborator; nil ⇒
	// /capabilities answers 503.
	Capabilities CapabilitiesController
	// SwarmStatus reports (known, capable) sn_search peer counts for /status;
	// nil ⇒ the swarm block stays zero.
	SwarmStatus func() (known, capable int)
	// PublisherStatus reports the Layer-D publisher state for GET /publish and
	// the /status publisher block; nil ⇒ empty publisher state (the PubKey
	// still renders from PublisherPubKey when an identity is loaded).
	PublisherStatus func() PublisherStatus
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
	mux.HandleFunc("GET /publish", s.handlePublish)
	s.torrentRoutes(mux)
	s.searchRoutes(mux)
	s.confirmFlagRoutes(mux)
	s.capabilitiesRoutes(mux)

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
	if s.opts.PublisherPubKey != nil {
		out.Publisher.PubKey = s.opts.PublisherPubKey()
	}
	if s.opts.LocalDocCount != nil {
		out.Local.Indexed = true
		if n, err := s.opts.LocalDocCount(); err == nil {
			out.Local.DocCount = n
		}
	}
	if s.opts.BloomStat != nil {
		out.Bloom = s.opts.BloomStat()
	}
	if s.opts.ReputationStat != nil {
		out.Reputation = s.opts.ReputationStat()
	}
	if s.opts.DHTStats != nil {
		good, total := s.opts.DHTStats()
		out.DHT = &DHTStatus{GoodNodes: good, Nodes: total}
	}
	if s.opts.SwarmStatus != nil {
		known, capable := s.opts.SwarmStatus()
		out.Swarm = SwarmStatus{KnownPeers: known, CapablePeers: capable}
	}
	if s.opts.PublisherStatus != nil {
		ps := s.opts.PublisherStatus()
		// PubKey is owned by PublisherPubKey (renders whenever an identity is
		// loaded, independent of the publisher); the totals come from the
		// publisher. The per-keyword list is reserved for GET /publish.
		out.Publisher.TotalKeywords = ps.TotalKeywords
		out.Publisher.TotalHits = ps.TotalHits
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// handlePublish renders the Layer-D publisher state, including the per-keyword
// list. The PubKey renders from PublisherPubKey whenever an identity is loaded,
// even before any publisher collaborator is wired.
func (s *Server) handlePublish(w http.ResponseWriter, _ *http.Request) {
	var out PublisherStatus
	if s.opts.PublisherPubKey != nil {
		out.PubKey = s.opts.PublisherPubKey()
	}
	if s.opts.PublisherStatus != nil {
		ps := s.opts.PublisherStatus()
		out.TotalKeywords = ps.TotalKeywords
		out.TotalHits = ps.TotalHits
		out.Keywords = ps.Keywords
	}
	writeJSON(w, out)
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(healthzResponse{OK: true, Version: s.opts.Version})
}
