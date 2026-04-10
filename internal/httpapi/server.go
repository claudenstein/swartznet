package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/swartznet/swartznet/internal/indexer"
	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// Server is the HTTP entry point into a running SwartzNet instance.
// Construct with New, call Start once, and Stop to tear down
// gracefully. A single Server can be reused across multiple
// Start/Stop cycles (a new http.Server is created each cycle).
type Server struct {
	addr    string
	log     *slog.Logger
	idx     *indexer.Index
	swarm   *swarmsearch.Protocol
	timeout time.Duration

	httpServer *http.Server
	listener   net.Listener
}

// New constructs a Server. Pass nil for idx or swarm to disable the
// corresponding search layer — requests for a disabled layer return
// an empty hit list rather than an error.
func New(addr string, idx *indexer.Index, swarm *swarmsearch.Protocol, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	if addr == "" {
		addr = "localhost:7654"
	}
	return &Server{
		addr:    addr,
		log:     log,
		idx:     idx,
		swarm:   swarm,
		timeout: 10 * time.Second,
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

	s.httpServer = &http.Server{
		Handler:     mux,
		ReadTimeout: 5 * time.Second,
	}
	go func() {
		if err := s.httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log.Warn("httpapi.serve_err", "err", err)
		}
	}()
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
	// SwarmTimeoutMs bounds the Layer-S fan-out. Zero → 2s default.
	SwarmTimeoutMs int `json:"swarm_timeout_ms,omitempty"`
}

// SearchResponse is the JSON body returned from POST /search. It is
// deliberately flat so that CLI clients can marshal it with encoding/json
// and print it without additional plumbing.
type SearchResponse struct {
	Local LocalResult `json:"local"`
	Swarm *SwarmResult `json:"swarm,omitempty"`
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

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}
