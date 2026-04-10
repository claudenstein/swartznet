package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/swartznet/swartznet/internal/dhtindex"
	"github.com/swartznet/swartznet/internal/indexer"
	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// Server is the HTTP entry point into a running SwartzNet instance.
// Construct with New, call Start once, and Stop to tear down
// gracefully. A single Server can be reused across multiple
// Start/Stop cycles (a new http.Server is created each cycle).
type Server struct {
	addr      string
	log       *slog.Logger
	idx       *indexer.Index
	swarm     *swarmsearch.Protocol
	publisher *dhtindex.Publisher
	lookup    *dhtindex.Lookup
	timeout   time.Duration

	httpServer *http.Server
	listener   net.Listener
}

// Options bundles the optional collaborators a Server can wire to.
// All fields are optional; the corresponding endpoints return empty
// or "not configured" responses when the field is nil.
type Options struct {
	Index     *indexer.Index
	Swarm     *swarmsearch.Protocol
	Publisher *dhtindex.Publisher
	Lookup    *dhtindex.Lookup
}

// New constructs a Server with the legacy index+swarm signature.
// New collaborators added in M4 (publisher, lookup) are optional;
// callers that need them should use NewWithOptions instead.
func New(addr string, idx *indexer.Index, swarm *swarmsearch.Protocol, log *slog.Logger) *Server {
	return NewWithOptions(addr, log, Options{Index: idx, Swarm: swarm})
}

// NewWithOptions constructs a Server from a richer Options bundle.
// Used by `swartznet add` to expose the publisher status and the
// DHT lookup via HTTP.
func NewWithOptions(addr string, log *slog.Logger, opts Options) *Server {
	if log == nil {
		log = slog.Default()
	}
	if addr == "" {
		addr = "localhost:7654"
	}
	return &Server{
		addr:      addr,
		log:       log,
		idx:       opts.Index,
		swarm:     opts.Swarm,
		publisher: opts.Publisher,
		lookup:    opts.Lookup,
		timeout:   10 * time.Second,
	}
}

// Addr returns the resolved listen address (useful when the caller
// passed "localhost:0" and wants to know which port got picked).
// Returns "" until Start has been called.
func (s *Server) Addr() string {
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// Start binds the listen socket and launches the HTTP server in a
// background goroutine. Returns as soon as the listener is bound
// so callers can reliably call Addr(). Errors from Serve are logged
// and returned through Stop().
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.listener = ln

	mux := http.NewServeMux()
	mux.HandleFunc("POST /search", s.handleSearch)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /status", s.handleStatus)

	srv := &http.Server{
		Handler:     mux,
		ReadTimeout: 5 * time.Second,
	}
	s.httpServer = srv
	// Pass srv as a parameter so the goroutine does not race
	// against Stop() resetting s.httpServer to nil. The goroutine
	// owns its own pointer; Stop still calls Shutdown on the same
	// underlying *http.Server through s.httpServer.
	go func(server *http.Server) {
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log.Warn("httpapi.serve_err", "err", err)
		}
	}(srv)
	s.log.Info("httpapi.listening", "addr", s.Addr())
	return nil
}

// Stop cleanly shuts down the HTTP server. Idempotent.
func (s *Server) Stop(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}
	err := s.httpServer.Shutdown(ctx)
	s.httpServer = nil
	s.listener = nil
	return err
}

// SearchRequest is the JSON body for POST /search.
type SearchRequest struct {
	Q     string `json:"q"`
	Limit int    `json:"limit,omitempty"`
	Swarm bool   `json:"swarm,omitempty"`
	// DHT, when true, also runs a Layer-D lookup against every
	// known indexer pubkey via dhtindex.Lookup. Hits are returned
	// in the response's "dht" section.
	DHT bool `json:"dht,omitempty"`
	// SwarmTimeoutMs bounds the Layer-S fan-out. Zero → 2s default.
	SwarmTimeoutMs int `json:"swarm_timeout_ms,omitempty"`
	// DHTTimeoutMs bounds the Layer-D get traversal. Zero → 5s.
	DHTTimeoutMs int `json:"dht_timeout_ms,omitempty"`
}

// SearchResponse is the JSON body returned from POST /search. It is
// deliberately flat so that CLI clients can marshal it with encoding/json
// and print it without additional plumbing.
type SearchResponse struct {
	Local LocalResult  `json:"local"`
	Swarm *SwarmResult `json:"swarm,omitempty"`
	DHT   *DHTResult   `json:"dht,omitempty"`
}

// DHTResult is the merged dhtindex.LookupResponse in JSON form.
type DHTResult struct {
	IndexersAsked     int      `json:"indexers_asked"`
	IndexersResponded int      `json:"indexers_responded"`
	Hits              []DHTHit `json:"hits"`
	Error             string   `json:"error,omitempty"`
}

type DHTHit struct {
	InfoHash string   `json:"infohash"`
	Name     string   `json:"name,omitempty"`
	Seeders  int      `json:"seeders,omitempty"`
	Size     int64    `json:"size,omitempty"`
	Files    int      `json:"files,omitempty"`
	Sources  []string `json:"sources,omitempty"`
}

// LocalResult mirrors indexer.SearchResponse in JSON-friendly form.
type LocalResult struct {
	Total int     `json:"total"`
	Hits  []LocalHit `json:"hits"`
}

type LocalHit struct {
	DocType   string `json:"doc_type"`
	InfoHash  string `json:"infohash"`
	Name      string `json:"name,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	FileIndex int    `json:"file_index,omitempty"`
	FilePath  string `json:"file_path,omitempty"`
	Mime      string `json:"mime,omitempty"`
	Extractor string `json:"extractor,omitempty"`
	Score     float64 `json:"score"`
}

// SwarmResult is the merged swarmsearch.QueryResponse in JSON form.
type SwarmResult struct {
	Asked     int         `json:"asked"`
	Responded int         `json:"responded"`
	Rejected  int         `json:"rejected"`
	Hits      []SwarmHit  `json:"hits"`
	Error     string      `json:"error,omitempty"`
}

type SwarmHit struct {
	InfoHash string   `json:"infohash"`
	Name     string   `json:"name,omitempty"`
	Size     int64    `json:"size,omitempty"`
	Seeders  int      `json:"seeders,omitempty"`
	Score    int      `json:"score"`
	Sources  []string `json:"sources,omitempty"`
}

// handleSearch is the main endpoint. It runs the local and swarm
// queries in parallel where possible, then writes a combined JSON
// response.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	var req SearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Q == "" {
		http.Error(w, "missing query field 'q'", http.StatusBadRequest)
		return
	}
	if req.Limit <= 0 {
		req.Limit = 50
	}

	resp := SearchResponse{Local: LocalResult{Hits: []LocalHit{}}}

	// Local search — always available if an index is attached.
	if s.idx != nil {
		res, err := s.idx.Search(indexer.SearchRequest{Query: req.Q, Limit: req.Limit})
		if err != nil {
			s.log.Warn("httpapi.local_err", "err", err)
			http.Error(w, "local search failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		resp.Local.Total = int(res.Total)
		for _, h := range res.Hits {
			resp.Local.Hits = append(resp.Local.Hits, LocalHit{
				DocType:   h.DocType,
				InfoHash:  h.InfoHash,
				Name:      h.Name,
				SizeBytes: h.SizeBytes,
				FileIndex: h.FileIndex,
				FilePath:  h.FilePath,
				Mime:      h.Mime,
				Extractor: h.Extractor,
				Score:     h.Score,
			})
		}
	}

	// Swarm search — optional, requires search-capable peers.
	if req.Swarm && s.swarm != nil {
		timeout := time.Duration(req.SwarmTimeoutMs) * time.Millisecond
		if timeout == 0 {
			timeout = 2 * time.Second
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout+500*time.Millisecond)
		defer cancel()
		out, err := s.swarm.Query(ctx, swarmsearch.QueryRequest{
			Q:            req.Q,
			PerPeerLimit: req.Limit,
			Timeout:      timeout,
		})
		swarmResp := &SwarmResult{Hits: []SwarmHit{}}
		if err != nil {
			swarmResp.Error = err.Error()
		} else {
			swarmResp.Asked = out.Asked
			swarmResp.Responded = out.Responded
			swarmResp.Rejected = out.Rejected
			for _, h := range out.Hits {
				swarmResp.Hits = append(swarmResp.Hits, SwarmHit{
					InfoHash: h.InfoHash,
					Name:     h.Name,
					Size:     h.Size,
					Seeders:  h.Seeders,
					Score:    h.Score,
					Sources:  h.Sources,
				})
			}
		}
		resp.Swarm = swarmResp
	}

	// DHT lookup — optional, requires a configured Lookup.
	if req.DHT && s.lookup != nil {
		timeout := time.Duration(req.DHTTimeoutMs) * time.Millisecond
		if timeout == 0 {
			timeout = 5 * time.Second
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout+500*time.Millisecond)
		defer cancel()
		out, err := s.lookup.Query(ctx, req.Q)
		dhtResp := &DHTResult{Hits: []DHTHit{}}
		if err != nil {
			dhtResp.Error = err.Error()
		} else {
			dhtResp.IndexersAsked = out.IndexersAsked
			dhtResp.IndexersResponded = out.IndexersResponded
			for _, h := range out.Hits {
				dhtResp.Hits = append(dhtResp.Hits, DHTHit{
					InfoHash: h.InfoHash,
					Name:     h.Name,
					Seeders:  h.Seeders,
					Size:     h.Size,
					Files:    h.Files,
					Sources:  h.Sources,
				})
			}
		}
		resp.DHT = dhtResp
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// StatusResponse is the JSON body returned from GET /status. It
// summarises the running daemon's local index, swarm peer set, and
// DHT publisher state.
type StatusResponse struct {
	Local     LocalStatus     `json:"local"`
	Swarm     SwarmStatus     `json:"swarm"`
	Publisher PublisherStatus `json:"publisher"`
}

type LocalStatus struct {
	Indexed bool   `json:"indexed"`
	DocCount uint64 `json:"doc_count"`
}

type SwarmStatus struct {
	KnownPeers   int `json:"known_peers"`
	CapablePeers int `json:"capable_peers"`
}

type PublisherStatus struct {
	PubKey        string                  `json:"pubkey,omitempty"`
	TotalKeywords int                     `json:"total_keywords"`
	TotalHits     int                     `json:"total_hits"`
	Keywords      []PublisherKeywordEntry `json:"keywords,omitempty"`
}

type PublisherKeywordEntry struct {
	Keyword       string `json:"keyword"`
	HitsCount     int    `json:"hits_count"`
	LastPublished string `json:"last_published,omitempty"`
	PublishCount  int    `json:"publish_count"`
	LastError     string `json:"last_error,omitempty"`
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	out := StatusResponse{}
	if s.idx != nil {
		out.Local.Indexed = true
		if n, err := s.idx.DocCount(); err == nil {
			out.Local.DocCount = n
		}
	}
	if s.swarm != nil {
		peers := s.swarm.KnownPeers()
		out.Swarm.KnownPeers = len(peers)
		out.Swarm.CapablePeers = s.swarm.CapablePeerCount()
	}
	if s.publisher != nil {
		st := s.publisher.Status()
		out.Publisher.TotalKeywords = st.TotalKeywords
		out.Publisher.TotalHits = st.TotalHits
		for _, ks := range st.LastPublishes {
			entry := PublisherKeywordEntry{
				Keyword:      ks.Keyword,
				HitsCount:    ks.HitsCount,
				PublishCount: ks.PublishCount,
				LastError:    ks.LastError,
			}
			if !ks.LastPublished.IsZero() {
				entry.LastPublished = ks.LastPublished.UTC().Format(time.RFC3339)
			}
			out.Publisher.Keywords = append(out.Publisher.Keywords, entry)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}
