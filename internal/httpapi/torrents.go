package httpapi

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
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
	RemoveTorrent(infohash string) error
	UploadLimitBytesPerSec() int64
	DownloadLimitBytesPerSec() int64
	SetUploadLimitBytesPerSec(bps int64)
	SetDownloadLimitBytesPerSec(bps int64)
	MaxActiveDownloads() int
	SetMaxActiveDownloads(n int)
}

func (s *Server) torrentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /torrent", s.handleAddTorrent)
	mux.HandleFunc("GET /torrents", s.handleTorrents)
	mux.HandleFunc("GET /torrents/{infohash}/files", s.handleTorrentFiles)
	mux.HandleFunc("POST /torrents/{infohash}/files/{index}/priority", s.handleSetFilePriority)
	mux.HandleFunc("POST /torrents/{infohash}/pause", s.torrentAction("pause"))
	mux.HandleFunc("POST /torrents/{infohash}/resume", s.torrentAction("resume"))
	mux.HandleFunc("DELETE /torrents/{infohash}", s.torrentAction("remove"))
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
		case "remove":
			err = s.opts.Control.RemoveTorrent(ih)
		}
		if err != nil {
			http.Error(w, fmt.Sprintf("%s: %v", action, err), http.StatusBadRequest)
			return
		}
		s.log.Info("httpapi.torrent_control", "action", action, "infohash", ih)
		writeJSON(w, map[string]any{"ok": true, "action": action, "infohash": ih})
	}
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
