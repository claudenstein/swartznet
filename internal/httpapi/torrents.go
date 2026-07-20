package httpapi

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// TorrentAdder accepts magnet URIs. The HTTP add surface is deliberately
// magnet-only; .torrent and infohash adds are in-process (CLI) forms.
type TorrentAdder interface {
	AddMagnetURI(uri string) (infohash string, err error)
}

// TorrentController is the torrent control surface, declared here (not
// imported): the daemon satisfies it with an engine-backed adapter.
type TorrentController interface {
	TorrentAdder
	TorrentSnapshots() []TorrentSnapshot
	TorrentFiles(infohash string) ([]TorrentFile, error)
	SetFilePriority(infohash string, index int, priority string) error
	PauseTorrent(infohash string) error
	ResumeTorrent(infohash string) error
	// RemoveTorrent drops the torrent (files kept). When forget is true it
	// also deletes the torrent's index documents — the reachable "Forget".
	RemoveTorrent(infohash string, forget bool) error
	SetTorrentIndexing(infohash string, enabled bool) error
	UploadLimitBytesPerSec() int64
	DownloadLimitBytesPerSec() int64
	SetUploadLimitBytesPerSec(bps int64)
	SetDownloadLimitBytesPerSec(bps int64)
	MaxActiveDownloads() int
	SetMaxActiveDownloads(n int)
}

func (s *Server) torrentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /torrent", s.handleAddTorrent)
	mux.HandleFunc("POST /torrents/create", s.handleCreateTorrent)
	mux.HandleFunc("GET /torrents", s.handleTorrents)
	mux.HandleFunc("GET /torrents/{infohash}/files", s.handleTorrentFiles)
	mux.HandleFunc("POST /torrents/{infohash}/files/{index}/priority", s.handleSetFilePriority)
	mux.HandleFunc("POST /torrents/{infohash}/pause", s.torrentAction("pause"))
	mux.HandleFunc("POST /torrents/{infohash}/resume", s.torrentAction("resume"))
	mux.HandleFunc("DELETE /torrents/{infohash}", s.handleRemove)
	mux.HandleFunc("POST /torrents/{infohash}/indexing", s.handleSetIndexing)
	mux.HandleFunc("GET /config/rate-limit", s.handleGetRateLimit)
	mux.HandleFunc("POST /config/rate-limit", s.handleSetRateLimit)
	mux.HandleFunc("PATCH /config/rate-limit", s.handleSetRateLimit)
	mux.HandleFunc("GET /config/queue", s.handleGetQueueConfig)
	mux.HandleFunc("POST /config/queue", s.handleSetQueueConfig)
	mux.HandleFunc("PATCH /config/queue", s.handleSetQueueConfig)
}

func (s *Server) handleAddTorrent(w http.ResponseWriter, r *http.Request) {
	if s.opts.Adder == nil {
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
	ih, err := s.opts.Adder.AddMagnetURI(req.URI)
	if err != nil {
		http.Error(w, "add: "+err.Error(), http.StatusBadRequest)
		return
	}
	s.log.Info("httpapi.add_torrent", "infohash", ih)
	writeJSON(w, AddTorrentResponse{OK: true, InfoHash: ih})
}

// handleCreateTorrent builds a .torrent from a daemon-side file/folder. root and
// output are paths on the daemon's machine (localhost operator); the collaborator
// hashes, optionally signs with the node identity, and optionally seeds in place.
func (s *Server) handleCreateTorrent(w http.ResponseWriter, r *http.Request) {
	if s.opts.CreateTorrent == nil {
		http.Error(w, "torrent creation not configured", http.StatusServiceUnavailable)
		return
	}
	var req CreateTorrentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	req.Root = strings.TrimSpace(req.Root)
	req.Output = strings.TrimSpace(req.Output)
	if req.Root == "" || req.Output == "" {
		http.Error(w, "both 'root' (file/folder) and 'output' (.torrent path) are required", http.StatusBadRequest)
		return
	}
	// Hashing a large folder can exceed the server WriteTimeout; extend the write
	// deadline for THIS response so a long, legitimate create isn't cut off. Best
	// effort — ignore the error if the platform doesn't support it.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(1 * time.Hour))

	res, err := s.opts.CreateTorrent(CreateTorrentParams{
		Root:     req.Root,
		Output:   req.Output,
		Trackers: req.Trackers,
		Comment:  strings.TrimSpace(req.Comment),
		Private:  req.Private,
		Sign:     req.Sign,
		Seed:     req.Seed,
	})
	if err != nil {
		http.Error(w, "create: "+err.Error(), http.StatusBadRequest)
		return
	}
	res.OK = true
	res.Output = req.Output
	s.log.Info("httpapi.create_torrent", "infohash", res.InfoHash, "output", req.Output, "seeded", res.Seeded)
	writeJSON(w, res)
}

func (s *Server) handleTorrents(w http.ResponseWriter, _ *http.Request) {
	if s.opts.Control == nil {
		http.Error(w, "torrent controller not configured", http.StatusServiceUnavailable)
		return
	}
	snaps := s.opts.Control.TorrentSnapshots()
	if snaps == nil {
		snaps = make([]TorrentSnapshot, 0)
	}
	writeJSON(w, TorrentsResponse{Torrents: snaps})
}

// pathInfohash validates the {infohash} path segment: 40 hex characters
// (case-insensitive here; the engine lookup canonicalizes).
func pathInfohash(w http.ResponseWriter, r *http.Request) (string, bool) {
	ih := r.PathValue("infohash")
	if len(ih) != 40 {
		http.Error(w, "infohash must be 40 hex characters", http.StatusBadRequest)
		return "", false
	}
	if _, err := hex.DecodeString(ih); err != nil {
		http.Error(w, "infohash must be 40 hex characters", http.StatusBadRequest)
		return "", false
	}
	return ih, true
}

func (s *Server) handleTorrentFiles(w http.ResponseWriter, r *http.Request) {
	if s.opts.Control == nil {
		http.Error(w, "torrent controller not configured", http.StatusServiceUnavailable)
		return
	}
	ih, ok := pathInfohash(w, r)
	if !ok {
		return
	}
	files, err := s.opts.Control.TorrentFiles(ih)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if files == nil {
		files = make([]TorrentFile, 0)
	}
	writeJSON(w, FilesListResponse{InfoHash: ih, Files: files})
}

func (s *Server) handleSetFilePriority(w http.ResponseWriter, r *http.Request) {
	if s.opts.Control == nil {
		http.Error(w, "torrent controller not configured", http.StatusServiceUnavailable)
		return
	}
	ih, ok := pathInfohash(w, r)
	if !ok {
		return
	}
	idx, err := strconv.Atoi(r.PathValue("index"))
	if err != nil || idx < 0 {
		http.Error(w, "file index must be a non-negative integer", http.StatusBadRequest)
		return
	}
	var req struct {
		Priority string `json:"priority"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.opts.Control.SetFilePriority(ih, idx, req.Priority); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "infohash": ih, "index": idx, "priority": req.Priority})
}

// torrentAction builds the pause/resume/remove handlers, sharing one shape.
func (s *Server) torrentAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.opts.Control == nil {
			http.Error(w, "torrent controller not configured", http.StatusServiceUnavailable)
			return
		}
		ih, ok := pathInfohash(w, r)
		if !ok {
			return
		}
		var err error
		switch action {
		case "pause":
			err = s.opts.Control.PauseTorrent(ih)
		case "resume":
			err = s.opts.Control.ResumeTorrent(ih)
		}
		if err != nil {
			http.Error(w, fmt.Sprintf("%s: %v", action, err), http.StatusBadRequest)
			return
		}
		s.log.Info("httpapi.torrent_control", "action", action, "infohash", ih)
		writeJSON(w, map[string]any{"ok": true, "action": action, "infohash": ih})
	}
}

// handleRemove drops a torrent, optionally forgetting its index docs
// (?forget=1). Files are always kept on disk.
func (s *Server) handleRemove(w http.ResponseWriter, r *http.Request) {
	if s.opts.Control == nil {
		http.Error(w, "torrent controller not configured", http.StatusServiceUnavailable)
		return
	}
	ih, ok := pathInfohash(w, r)
	if !ok {
		return
	}
	forget := r.URL.Query().Get("forget") == "1"
	if err := s.opts.Control.RemoveTorrent(ih, forget); err != nil {
		http.Error(w, "remove: "+err.Error(), http.StatusBadRequest)
		return
	}
	s.log.Info("httpapi.torrent_control", "action", "remove", "infohash", ih, "forget", forget)
	writeJSON(w, map[string]any{"ok": true, "action": "remove", "infohash": ih, "forgot": forget})
}

// handleSetIndexing toggles per-torrent indexing.
func (s *Server) handleSetIndexing(w http.ResponseWriter, r *http.Request) {
	if s.opts.Control == nil {
		http.Error(w, "torrent controller not configured", http.StatusServiceUnavailable)
		return
	}
	ih, ok := pathInfohash(w, r)
	if !ok {
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.opts.Control.SetTorrentIndexing(ih, req.Enabled); err != nil {
		http.Error(w, "indexing: "+err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "infohash": ih, "enabled": req.Enabled})
}

func (s *Server) handleGetRateLimit(w http.ResponseWriter, _ *http.Request) {
	if s.opts.Control == nil {
		http.Error(w, "torrent controller not configured", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, RateLimitResponse{
		UploadBps:   s.opts.Control.UploadLimitBytesPerSec(),
		DownloadBps: s.opts.Control.DownloadLimitBytesPerSec(),
	})
}

// handleSetRateLimit merges: an absent field leaves that limiter untouched
// (the legacy decoded omitted fields as 0 and cleared them — §6); a present
// value ≤0 means unlimited. The response is the fresh GET body.
func (s *Server) handleSetRateLimit(w http.ResponseWriter, r *http.Request) {
	if s.opts.Control == nil {
		http.Error(w, "torrent controller not configured", http.StatusServiceUnavailable)
		return
	}
	var req RateLimitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.UploadBps != nil {
		s.opts.Control.SetUploadLimitBytesPerSec(*req.UploadBps)
	}
	if req.DownloadBps != nil {
		s.opts.Control.SetDownloadLimitBytesPerSec(*req.DownloadBps)
	}
	s.handleGetRateLimit(w, r)
}

func (s *Server) handleGetQueueConfig(w http.ResponseWriter, _ *http.Request) {
	if s.opts.Control == nil {
		http.Error(w, "torrent controller not configured", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, QueueConfigResponse{MaxActiveDownloads: s.opts.Control.MaxActiveDownloads()})
}

func (s *Server) handleSetQueueConfig(w http.ResponseWriter, r *http.Request) {
	if s.opts.Control == nil {
		http.Error(w, "torrent controller not configured", http.StatusServiceUnavailable)
		return
	}
	var req QueueConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.MaxActiveDownloads != nil {
		s.opts.Control.SetMaxActiveDownloads(*req.MaxActiveDownloads)
	}
	s.handleGetQueueConfig(w, r)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
