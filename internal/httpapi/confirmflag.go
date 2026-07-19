package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
)

// Error sentinels the daemon's Confirm/Flag return, mapped to HTTP status
// here so the daemon package need not know HTTP codes.
var (
	ErrBadInfohash          = errors.New("infohash must be 40 hex characters")
	ErrBloomNotConfigured   = errors.New("bloom filter not configured")
	ErrTrackerNotConfigured = errors.New("reputation tracker not configured")
)

func (s *Server) confirmFlagRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /confirm", s.handleConfirm)
	mux.HandleFunc("POST /flag", s.handleFlag)
	mux.HandleFunc("GET /aggregate", s.handleAggregate)
}

func (s *Server) handleConfirm(w http.ResponseWriter, r *http.Request) {
	if s.opts.Confirm == nil {
		http.Error(w, "bloom filter not configured", http.StatusServiceUnavailable)
		return
	}
	var req FlagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	res, err := s.opts.Confirm(req.InfoHash)
	if err != nil {
		writeConfirmFlagErr(w, err)
		return
	}
	writeJSON(w, ConfirmResponse{OK: true, InfoHash: res.InfoHash, IndexersConfirmed: res.IndexersConfirmed})
}

func (s *Server) handleFlag(w http.ResponseWriter, r *http.Request) {
	if s.opts.Flag == nil {
		http.Error(w, "reputation tracker not configured", http.StatusServiceUnavailable)
		return
	}
	var req FlagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	res, err := s.opts.Flag(req.InfoHash)
	if err != nil {
		writeConfirmFlagErr(w, err)
		return
	}
	writeJSON(w, FlagResponse{OK: true, InfoHash: res.InfoHash, IndexersFlagged: res.IndexersFlagged, Attribution: res.Attribution})
}

// writeConfirmFlagErr maps the shared-path error sentinels to HTTP codes.
func writeConfirmFlagErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrBadInfohash):
		http.Error(w, ErrBadInfohash.Error(), http.StatusBadRequest)
	case errors.Is(err, ErrBloomNotConfigured):
		http.Error(w, ErrBloomNotConfigured.Error(), http.StatusServiceUnavailable)
	case errors.Is(err, ErrTrackerNotConfigured):
		http.Error(w, ErrTrackerNotConfigured.Error(), http.StatusServiceUnavailable)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleAggregate(w http.ResponseWriter, _ *http.Request) {
	if s.opts.Aggregate == nil {
		http.Error(w, "aggregate not configured", http.StatusServiceUnavailable)
		return
	}
	resp := s.opts.Aggregate()
	// The services mask has a single render path: the live reporter, never a
	// static constant (the §6 static-0x2ED fix).
	resp.Services = s.servicesHex()
	writeJSON(w, resp)
}
