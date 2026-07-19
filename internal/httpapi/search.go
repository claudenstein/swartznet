package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
)

const (
	maxSearchQueryBytes = 1024
	defaultSearchLimit  = 50
	maxSearchLimit      = 500
)

func (s *Server) searchRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /search", s.handleSearch)
	mux.HandleFunc("GET /index/stats", s.handleIndexStats)
}

// handleSearch runs Layer L (and, later, S/D). A nil Search collaborator is
// NOT a 503: it answers 200 with an empty local block — Layer L is simply
// off. A Layer-L error IS fatal (500); swarm/DHT errors surface inline
// (later slices).
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	var req SearchRequestBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Q == "" {
		http.Error(w, "missing query field 'q'", http.StatusBadRequest)
		return
	}
	if len(req.Q) > maxSearchQueryBytes {
		http.Error(w, fmt.Sprintf("query too long: %d bytes (max %d)", len(req.Q), maxSearchQueryBytes), http.StatusBadRequest)
		return
	}
	limit := req.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}

	out := SearchResponse{Local: LocalBlock{Hits: []LocalHit{}}}
	if s.opts.Search != nil {
		res := s.opts.Search(SearchParams{
			Query:          req.Q,
			Limit:          limit,
			SignedBy:       req.SignedBy,
			Highlight:      req.Highlight,
			Swarm:          req.Swarm,
			SwarmTimeoutMS: req.SwarmTimeout,
			DHT:            req.DHT,
			DHTTimeoutMS:   req.DHTTimeout,
		})
		if res.LocalErr != nil {
			s.log.Warn("httpapi.local_err", "err", res.LocalErr)
			http.Error(w, "local search failed: "+res.LocalErr.Error(), http.StatusInternalServerError)
			return
		}
		out.Local = res.Local
		if out.Local.Hits == nil {
			out.Local.Hits = []LocalHit{}
		}
		// Layer-S AND Layer-D failures are surfaced inline (a 200 with the
		// block's error string), never a 5xx (§5.9). Each block appears only
		// when the adapter produced one.
		out.Swarm = res.Swarm
		out.Dht = res.Dht
	}
	writeJSON(w, out)
}

func (s *Server) handleIndexStats(w http.ResponseWriter, _ *http.Request) {
	if s.opts.IndexStats == nil {
		http.Error(w, "index not configured", http.StatusServiceUnavailable)
		return
	}
	stats, err := s.opts.IndexStats()
	if err != nil {
		http.Error(w, "stats: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, stats)
}
