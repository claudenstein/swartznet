package httpapi

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/swartznet/swartznet/internal/dhtindex"
	"github.com/swartznet/swartznet/internal/httpapi/web"
	"github.com/swartznet/swartznet/internal/indexer"
	"github.com/swartznet/swartznet/internal/reputation"
	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// TorrentController is the narrow interface the HTTP API needs
// from the engine for the M10 GUI download controls. The engine
// satisfies it via direct methods on *engine.Engine.
//
// Defined as a local interface so the httpapi package keeps zero
// dependency on internal/engine — the adapter pattern is the
// same one used for indexer + swarmsearch + dhtindex. Embeds
// the older TorrentAdder for backwards compatibility with the
// M8c POST /torrent path.
type TorrentController interface {
	TorrentAdder
	// TorrentSnapshots returns a snapshot of every active
	// torrent. Cheap to call from a polling handler.
	TorrentSnapshots() []TorrentSnapshot
	// PauseTorrent stops piece requests for the given infohash.
	// Idempotent.
	PauseTorrent(infoHashHex string) error
	// ResumeTorrent re-enables piece requests. Idempotent.
	ResumeTorrent(infoHashHex string) error
	// RemoveTorrent drops the torrent entirely (closes peer
	// connections, forgets piece state). On-disk file content
	// is left in place.
	RemoveTorrent(infoHashHex string) error
	// SetTorrentIndexing flips the per-torrent indexing toggle.
	// Idempotent.
	SetTorrentIndexing(infoHashHex string, enabled bool) error
	// TorrentFiles returns a per-file snapshot of the torrent.
	// Empty slice if the torrent has no metadata yet.
	TorrentFiles(infoHashHex string) ([]TorrentFile, error)
	// SetFilePriority flips a single file's download priority.
	// priority must be "none", "normal", or "high".
	SetFilePriority(infoHashHex string, fileIndex int, priority string) error
}

// TorrentFile mirrors engine.FileSnapshot so the httpapi layer
// stays free of an import on internal/engine.
type TorrentFile struct {
	Index          int     `json:"index"`
	Path           string  `json:"path"`
	DisplayPath    string  `json:"display_path"`
	Length         int64   `json:"length"`
	BytesCompleted int64   `json:"bytes_completed"`
	Progress       float64 `json:"progress"`
	Priority       string  `json:"priority"`
}

// TorrentSnapshot mirrors engine.TorrentSnapshot. Re-declared
// here so the httpapi package does not import internal/engine.
// The engine's snapshotter returns a slice of these directly.
type TorrentSnapshot struct {
	InfoHash         string  `json:"infohash"`
	Name             string  `json:"name"`
	Size             int64   `json:"size"`
	BytesCompleted   int64   `json:"bytes_completed"`
	BytesMissing     int64   `json:"bytes_missing"`
	Progress         float64 `json:"progress"`
	Files            int     `json:"files"`
	ActivePeers      int     `json:"active_peers"`
	HalfOpenPeers    int     `json:"half_open_peers"`
	PendingPeers     int     `json:"pending_peers"`
	TotalPeers       int     `json:"total_peers"`
	Seeders          int     `json:"seeders"`
	Paused           bool    `json:"paused"`
	Status           string  `json:"status"`
	Indexing         bool    `json:"indexing"`
	Queued           bool    `json:"queued"`
	DownloadRate     int64   `json:"download_rate"`
	UploadRate       int64   `json:"upload_rate"`
	SignedBy         string  `json:"signed_by,omitempty"`
	TrustedPublisher bool    `json:"trusted_publisher,omitempty"`
}

// TorrentAdder is the narrow interface the HTTP API needs from
// the engine in order to satisfy POST /torrent. The engine
// satisfies it via an adapter method (see internal/engine).
//
// Defined as a local interface so the httpapi package keeps zero
// dependency on internal/engine — the adapter pattern is the
// same one used for indexer + swarmsearch + dhtindex.
type TorrentAdder interface {
	// AddMagnetURI queues the magnet URI for download and
	// returns its infohash as a 40-char lowercase hex string.
	// Returns immediately; metadata fetch happens
	// asynchronously in the background.
	AddMagnetURI(uri string) (infohash string, err error)
}

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
	bloom     *reputation.BloomFilter
	tracker   *reputation.Tracker
	sources   *reputation.SourceTracker
	adder     TorrentAdder
	control   TorrentController
	companion CompanionController
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
	Bloom     *reputation.BloomFilter
	Tracker   *reputation.Tracker
	Sources   *reputation.SourceTracker
	Adder     TorrentAdder
	Control   TorrentController
	Companion CompanionController
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
		bloom:     opts.Bloom,
		tracker:   opts.Tracker,
		sources:   opts.Sources,
		adder:     opts.Adder,
		control:   opts.Control,
		companion: opts.Companion,
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
	mux.HandleFunc("POST /confirm", s.handleConfirm)
	mux.HandleFunc("POST /flag", s.handleFlag)
	mux.HandleFunc("POST /torrent", s.handleAddTorrent)
	mux.HandleFunc("GET /capabilities", s.handleGetCapabilities)
	mux.HandleFunc("POST /capabilities", s.handleSetCapabilities)
	mux.HandleFunc("GET /torrents", s.handleListTorrents)
	mux.HandleFunc("POST /torrents/{infohash}/pause", s.handlePauseTorrent)
	mux.HandleFunc("POST /torrents/{infohash}/resume", s.handleResumeTorrent)
	mux.HandleFunc("DELETE /torrents/{infohash}", s.handleDeleteTorrent)
	mux.HandleFunc("POST /torrents/{infohash}/indexing", s.handleSetTorrentIndexing)
	mux.HandleFunc("GET /torrents/{infohash}/files", s.handleListTorrentFiles)
	mux.HandleFunc("POST /torrents/{infohash}/files/{index}/priority", s.handleSetFilePriority)
	mux.HandleFunc("GET /companion", s.handleCompanionStatus)
	mux.HandleFunc("POST /companion/refresh", s.handleCompanionRefresh)
	mux.HandleFunc("POST /companion/follow", s.handleCompanionFollow)
	mux.HandleFunc("POST /companion/unfollow", s.handleCompanionUnfollow)
	mux.HandleFunc("GET /index/stats", s.handleIndexStats)

	// Web UI: serve the embedded index.html at / and the
	// static assets at /static/. The HTTP API endpoints above
	// are registered first so they take precedence over the
	// catch-all root handler.
	if assetsFS, err := fs.Sub(web.Assets(), "."); err == nil {
		fileServer := http.FileServer(http.FS(assetsFS))
		mux.Handle("GET /static/", fileServer)
		mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
			http.ServeFileFS(w, r, assetsFS, "index.html")
		})
	} else {
		s.log.Warn("httpapi.web_assets_unavailable", "err", err)
	}

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
	// Highlight, when true, asks the local indexer to return
	// matched-text fragments on each hit (LocalHit.Fragments).
	// Fragments are wrapped with <mark>...</mark> by Bleve's
	// HTML highlighter. The default is false to keep the
	// response small for programmatic callers; the web UI
	// always sets it.
	Highlight bool `json:"highlight,omitempty"`
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
	Total int        `json:"total"`
	Hits  []LocalHit `json:"hits"`
}

type LocalHit struct {
	DocType   string  `json:"doc_type"`
	InfoHash  string  `json:"infohash"`
	Name      string  `json:"name,omitempty"`
	SizeBytes int64   `json:"size_bytes,omitempty"`
	FileIndex int     `json:"file_index,omitempty"`
	FilePath  string  `json:"file_path,omitempty"`
	Mime      string  `json:"mime,omitempty"`
	Extractor string  `json:"extractor,omitempty"`
	Score     float64 `json:"score"`
	// Fragments maps a Bleve field name to a list of matched
	// text fragments, pre-wrapped by Bleve's HTML highlighter
	// so that matching terms appear as <mark>term</mark>.
	// Populated only when SearchRequest.Highlight is true.
	Fragments map[string][]string `json:"fragments,omitempty"`
}

// SwarmResult is the merged swarmsearch.QueryResponse in JSON form.
type SwarmResult struct {
	Asked     int        `json:"asked"`
	Responded int        `json:"responded"`
	Rejected  int        `json:"rejected"`
	Hits      []SwarmHit `json:"hits"`
	Error     string     `json:"error,omitempty"`
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
		res, err := s.idx.Search(indexer.SearchRequest{
			Query:     req.Q,
			Limit:     req.Limit,
			Highlight: req.Highlight,
		})
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
				Fragments: h.Fragments,
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
// summarises the running daemon's local index, swarm peer set,
// DHT publisher state, and the M5 spam-resistance helpers.
type StatusResponse struct {
	Local      LocalStatus     `json:"local"`
	Swarm      SwarmStatus     `json:"swarm"`
	Publisher  PublisherStatus `json:"publisher"`
	Bloom      *BloomStatus    `json:"bloom,omitempty"`
	Reputation *ReputationStat `json:"reputation,omitempty"`
}

// BloomStatus is the M5 known-good Bloom filter view.
type BloomStatus struct {
	BitSize        uint64  `json:"bit_size"`
	HashFunctions  uint64  `json:"hash_functions"`
	PopulationBits uint64  `json:"population_bits"`
	EstimatedItems float64 `json:"estimated_items"`
}

// ReputationStat is the M5 per-pubkey reputation summary.
type ReputationStat struct {
	KnownIndexers int                        `json:"known_indexers"`
	TopIndexers   []ReputationIndexerSummary `json:"top_indexers,omitempty"`
}

// ReputationIndexerSummary is one row of the top-N indexer table.
type ReputationIndexerSummary struct {
	PubKey        string  `json:"pubkey"`
	Score         float64 `json:"score"`
	HitsReturned  int     `json:"hits_returned"`
	HitsConfirmed int     `json:"hits_confirmed"`
	HitsFlagged   int     `json:"hits_flagged"`
}

type LocalStatus struct {
	Indexed  bool   `json:"indexed"`
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

// IndexStats is the JSON shape for GET /index/stats. Mirrors
// indexer.Stats one-for-one but lives here so the httpapi package
// does not leak the indexer type into the HTTP response schema.
type IndexStats struct {
	DirBytes        int64   `json:"dir_bytes"`
	DocCount        uint64  `json:"doc_count"`
	TorrentCount    uint64  `json:"torrent_count"`
	ContentCount    uint64  `json:"content_count"`
	CorpusTextBytes int64   `json:"corpus_text_bytes"`
	InflationRatio  float64 `json:"inflation_ratio"`
}

// handleIndexStats serves GET /index/stats. The response is the
// v1.0.0 "how big is the Bleve index per TB of indexed text"
// measurement that anyone running the daemon can produce without
// scraping internal state. Cheap-ish — the corpus-bytes sum
// scans every content doc — so the GUI should poll at human
// cadences only.
func (s *Server) handleIndexStats(w http.ResponseWriter, _ *http.Request) {
	if s.idx == nil {
		http.Error(w, "index not configured", http.StatusServiceUnavailable)
		return
	}
	st, err := s.idx.Stats()
	if err != nil {
		http.Error(w, "stats: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(IndexStats{
		DirBytes:        st.DirBytes,
		DocCount:        st.DocCount,
		TorrentCount:    st.TorrentCount,
		ContentCount:    st.ContentCount,
		CorpusTextBytes: st.CorpusTextBytes,
		InflationRatio:  st.InflationRatio,
	})
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
	if s.bloom != nil {
		out.Bloom = &BloomStatus{
			BitSize:        s.bloom.Bits(),
			HashFunctions:  s.bloom.HashFunctions(),
			PopulationBits: s.bloom.PopulationCount(),
			EstimatedItems: s.bloom.EstimatedItems(),
		}
	}
	if s.tracker != nil {
		snap := s.tracker.Snapshot()
		rep := &ReputationStat{KnownIndexers: len(snap)}
		topN := snap
		if len(topN) > 10 {
			topN = topN[:10]
		}
		for _, e := range topN {
			rep.TopIndexers = append(rep.TopIndexers, ReputationIndexerSummary{
				PubKey:        string(e.PubKey),
				Score:         e.Score,
				HitsReturned:  e.Counters.HitsReturned,
				HitsConfirmed: e.Counters.HitsConfirmed,
				HitsFlagged:   e.Counters.HitsFlagged,
			})
		}
		out.Reputation = rep
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// FlagRequest / ConfirmRequest is the JSON body for the
// POST /flag and POST /confirm endpoints respectively. Both take
// a single 40-char hex infohash; the rest of the work happens
// inside the engine.
type FlagRequest struct {
	InfoHash string `json:"infohash"`
}

type ConfirmRequest = FlagRequest

// FlagResponse is the JSON body returned from /flag and /confirm.
type FlagResponse struct {
	OK       bool   `json:"ok"`
	InfoHash string `json:"infohash"`
}

func (s *Server) handleConfirm(w http.ResponseWriter, r *http.Request) {
	var req ConfirmRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	ih, err := hex.DecodeString(req.InfoHash)
	if err != nil || len(ih) != 20 {
		http.Error(w, "infohash must be 40 hex characters", http.StatusBadRequest)
		return
	}
	if s.bloom == nil {
		http.Error(w, "bloom filter not configured", http.StatusServiceUnavailable)
		return
	}
	s.bloom.Add(ih)
	s.log.Info("httpapi.confirm", "infohash", req.InfoHash)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(FlagResponse{OK: true, InfoHash: req.InfoHash})
}

func (s *Server) handleFlag(w http.ResponseWriter, r *http.Request) {
	var req FlagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	ih, err := hex.DecodeString(req.InfoHash)
	if err != nil || len(ih) != 20 {
		http.Error(w, "infohash must be 40 hex characters", http.StatusBadRequest)
		return
	}
	if s.tracker == nil {
		http.Error(w, "reputation tracker not configured", http.StatusServiceUnavailable)
		return
	}

	// M9: prefer per-hit source attribution. The SourceTracker
	// (populated by Lookup.Query) tells us exactly which indexer
	// pubkeys returned this hit, so we can demote only those
	// rather than punishing every known indexer indiscriminately.
	//
	// If no source attribution exists for this hash (the user
	// never queried it through Layer D, or it has been evicted
	// from the LRU), fall back to the M5d behaviour of demoting
	// every known indexer — it is the safest fallback because
	// any indexer that ends up claiming the same hash later will
	// also lose reputation, which is exactly what we want for a
	// hash the user has explicitly flagged.
	var pks []reputation.PubKeyHex
	var attribution string
	if s.sources != nil {
		pks = s.sources.Sources(req.InfoHash)
	}
	if len(pks) > 0 {
		attribution = "targeted"
	} else {
		attribution = "fallback"
		snap := s.tracker.Snapshot()
		pks = make([]reputation.PubKeyHex, 0, len(snap))
		for _, e := range snap {
			pks = append(pks, e.PubKey)
		}
	}
	s.tracker.RecordFlagged(pks...)
	if s.sources != nil {
		s.sources.Forget(req.InfoHash)
	}
	s.log.Info("httpapi.flag",
		"infohash", req.InfoHash,
		"indexers", len(pks),
		"attribution", attribution,
	)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(FlagResponse{OK: true, InfoHash: req.InfoHash})
}

// AddTorrentRequest is the JSON body for POST /torrent.
type AddTorrentRequest struct {
	URI string `json:"uri"`
}

// AddTorrentResponse is the JSON body returned by POST /torrent.
// The infohash is parsed from the magnet URI itself and is
// available immediately; metadata fetch from the swarm happens
// asynchronously after the response is sent.
type AddTorrentResponse struct {
	OK       bool   `json:"ok"`
	InfoHash string `json:"infohash"`
}

func (s *Server) handleAddTorrent(w http.ResponseWriter, r *http.Request) {
	if s.adder == nil {
		http.Error(w, "torrent adder not configured", http.StatusServiceUnavailable)
		return
	}
	var req AddTorrentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.URI == "" {
		http.Error(w, "missing 'uri' field", http.StatusBadRequest)
		return
	}
	infoHash, err := s.adder.AddMagnetURI(req.URI)
	if err != nil {
		http.Error(w, "add: "+err.Error(), http.StatusBadRequest)
		return
	}
	s.log.Info("httpapi.add_torrent", "infohash", infoHash)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(AddTorrentResponse{OK: true, InfoHash: infoHash})
}

// CapabilitiesBody is both the request body for POST /capabilities
// AND the response body for GET /capabilities. Field names match
// swarmsearch.Capabilities so the round trip is straightforward.
type CapabilitiesBody struct {
	ShareLocal  int `json:"share_local"`
	FileHits    int `json:"file_hits"`
	ContentHits int `json:"content_hits"`
	Publisher   int `json:"publisher"`
}

func (s *Server) handleGetCapabilities(w http.ResponseWriter, _ *http.Request) {
	if s.swarm == nil {
		http.Error(w, "swarmsearch not configured", http.StatusServiceUnavailable)
		return
	}
	c := s.swarm.Capabilities()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(CapabilitiesBody{
		ShareLocal:  c.ShareLocal,
		FileHits:    c.FileHits,
		ContentHits: c.ContentHits,
		Publisher:   c.Publisher,
	})
}

// TorrentListResponse is the JSON body for GET /torrents.
type TorrentListResponse struct {
	Torrents []TorrentSnapshot `json:"torrents"`
}

func (s *Server) handleListTorrents(w http.ResponseWriter, _ *http.Request) {
	if s.control == nil {
		http.Error(w, "torrent controller not configured", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(TorrentListResponse{
		Torrents: s.control.TorrentSnapshots(),
	})
}

func (s *Server) handlePauseTorrent(w http.ResponseWriter, r *http.Request) {
	s.controlOne(w, r, "pause", func(ih string) error {
		return s.control.PauseTorrent(ih)
	})
}

func (s *Server) handleResumeTorrent(w http.ResponseWriter, r *http.Request) {
	s.controlOne(w, r, "resume", func(ih string) error {
		return s.control.ResumeTorrent(ih)
	})
}

func (s *Server) handleDeleteTorrent(w http.ResponseWriter, r *http.Request) {
	s.controlOne(w, r, "remove", func(ih string) error {
		return s.control.RemoveTorrent(ih)
	})
}

// IndexingRequest is the body of POST /torrents/{infohash}/indexing.
type IndexingRequest struct {
	Enabled bool `json:"enabled"`
}

func (s *Server) handleSetTorrentIndexing(w http.ResponseWriter, r *http.Request) {
	if s.control == nil {
		http.Error(w, "torrent controller not configured", http.StatusServiceUnavailable)
		return
	}
	ihHex := r.PathValue("infohash")
	if _, err := hex.DecodeString(ihHex); err != nil || len(ihHex) != 40 {
		http.Error(w, "infohash must be 40 hex characters", http.StatusBadRequest)
		return
	}
	var body IndexingRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.control.SetTorrentIndexing(ihHex, body.Enabled); err != nil {
		http.Error(w, "indexing: "+err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":       true,
		"infohash": ihHex,
		"enabled":  body.Enabled,
	})
}

// FilesListResponse is the body of GET /torrents/{infohash}/files.
type FilesListResponse struct {
	InfoHash string        `json:"infohash"`
	Files    []TorrentFile `json:"files"`
}

func (s *Server) handleListTorrentFiles(w http.ResponseWriter, r *http.Request) {
	if s.control == nil {
		http.Error(w, "torrent controller not configured", http.StatusServiceUnavailable)
		return
	}
	ihHex := r.PathValue("infohash")
	if _, err := hex.DecodeString(ihHex); err != nil || len(ihHex) != 40 {
		http.Error(w, "infohash must be 40 hex characters", http.StatusBadRequest)
		return
	}
	files, err := s.control.TorrentFiles(ihHex)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(FilesListResponse{
		InfoHash: ihHex,
		Files:    files,
	})
}

// FilePriorityRequest is the body of POST
// /torrents/{infohash}/files/{index}/priority.
type FilePriorityRequest struct {
	Priority string `json:"priority"`
}

func (s *Server) handleSetFilePriority(w http.ResponseWriter, r *http.Request) {
	if s.control == nil {
		http.Error(w, "torrent controller not configured", http.StatusServiceUnavailable)
		return
	}
	ihHex := r.PathValue("infohash")
	if _, err := hex.DecodeString(ihHex); err != nil || len(ihHex) != 40 {
		http.Error(w, "infohash must be 40 hex characters", http.StatusBadRequest)
		return
	}
	idxStr := r.PathValue("index")
	var idx int
	if _, err := fmt.Sscanf(idxStr, "%d", &idx); err != nil || idx < 0 {
		http.Error(w, "file index must be a non-negative integer", http.StatusBadRequest)
		return
	}
	var body FilePriorityRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.control.SetFilePriority(ihHex, idx, body.Priority); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":       true,
		"infohash": ihHex,
		"index":    idx,
		"priority": body.Priority,
	})
}

// controlOne is the shared body for the three pause / resume /
// remove handlers. Validates the infohash, calls the supplied
// engine method, returns a {ok, infohash, action} JSON envelope.
func (s *Server) controlOne(w http.ResponseWriter, r *http.Request, action string, fn func(ih string) error) {
	if s.control == nil {
		http.Error(w, "torrent controller not configured", http.StatusServiceUnavailable)
		return
	}
	ihHex := r.PathValue("infohash")
	if _, err := hex.DecodeString(ihHex); err != nil || len(ihHex) != 40 {
		http.Error(w, "infohash must be 40 hex characters", http.StatusBadRequest)
		return
	}
	if err := fn(ihHex); err != nil {
		http.Error(w, action+": "+err.Error(), http.StatusBadRequest)
		return
	}
	s.log.Info("httpapi.torrent_control", "action", action, "infohash", ihHex)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":       true,
		"infohash": ihHex,
		"action":   action,
	})
}

func (s *Server) handleSetCapabilities(w http.ResponseWriter, r *http.Request) {
	if s.swarm == nil {
		http.Error(w, "swarmsearch not configured", http.StatusServiceUnavailable)
		return
	}
	var req CapabilitiesBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	// Clamp ShareLocal to {0,1,2}; FileHits/ContentHits/Publisher to {0,1}.
	clamp := func(v, lo, hi int) int {
		if v < lo {
			return lo
		}
		if v > hi {
			return hi
		}
		return v
	}
	s.swarm.SetCapabilities(swarmsearch.Capabilities{
		ShareLocal:  clamp(req.ShareLocal, 0, 2),
		FileHits:    clamp(req.FileHits, 0, 1),
		ContentHits: clamp(req.ContentHits, 0, 1),
		Publisher:   clamp(req.Publisher, 0, 1),
	})
	c := s.swarm.Capabilities()
	s.log.Info("httpapi.set_capabilities",
		"share", c.ShareLocal, "file", c.FileHits, "content", c.ContentHits)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(CapabilitiesBody{
		ShareLocal:  c.ShareLocal,
		FileHits:    c.FileHits,
		ContentHits: c.ContentHits,
		Publisher:   c.Publisher,
	})
}

// HealthzVersion is overridden by the swartznet binary at build
// time so /healthz can report the running version. Defaults to a
// dev placeholder when the field is not set.
var HealthzVersion = ""

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	type healthzBody struct {
		OK      bool   `json:"ok"`
		Version string `json:"version,omitempty"`
	}
	_ = json.NewEncoder(w).Encode(healthzBody{OK: true, Version: HealthzVersion})
}
