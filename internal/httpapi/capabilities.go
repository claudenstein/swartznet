package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// CapabilitiesController is the daemon-supplied collaborator for the sn_search
// sharing prefs. httpapi never imports the capability contract — it traffics in
// its own SharingPrefs DTO and renders the mask hex itself, preserving the
// zero-subsystem-import law. Publisher is read-only (a daemon runtime fact);
// there is no setter for it, which is the structural §6 clobber fix.
type CapabilitiesController interface {
	// Sharing returns the current operator prefs.
	Sharing() SharingPrefs
	// SetSharing stores the merged prefs (the server does the preserve-unset
	// merge before calling this).
	SetSharing(SharingPrefs)
	// Publisher reports the daemon-owned Publishing runtime fact.
	Publisher() bool
}

func (s *Server) capabilitiesRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /capabilities", s.handleGetCapabilities)
	// PATCH is the primary verb (preserve-unset merge); POST is an alias for
	// parity with /config/rate-limit and /config/queue.
	mux.HandleFunc("PATCH /capabilities", s.handleSetCapabilities)
	mux.HandleFunc("POST /capabilities", s.handleSetCapabilities)
}

// servicesHex renders the live 64-bit services mask as 16 lowercase hex chars.
// httpapi renders it locally (no contract import); nil reporter ⇒ all-zero.
func (s *Server) servicesHex() string {
	if s.opts.ServicesReporter != nil {
		return fmt.Sprintf("%016x", s.opts.ServicesReporter())
	}
	return "0000000000000000"
}

func (s *Server) handleGetCapabilities(w http.ResponseWriter, _ *http.Request) {
	if s.opts.Capabilities == nil {
		http.Error(w, "capabilities not configured", http.StatusServiceUnavailable)
		return
	}
	sp := s.opts.Capabilities.Sharing()
	writeJSON(w, CapabilitiesResponse{
		ShareLocal:  sp.ShareLocal,
		FileHits:    sp.FileHits,
		ContentHits: sp.ContentHits,
		Publisher:   s.opts.Capabilities.Publisher(),
		Services:    s.servicesHex(),
	})
}

// handleSetCapabilities merges: an absent field leaves that pref untouched (the
// legacy replace-whole-struct zeroed omitted fields — §6). It only ever touches
// Sharing; the daemon-owned Publisher bit is not in the request shape at all.
func (s *Server) handleSetCapabilities(w http.ResponseWriter, r *http.Request) {
	if s.opts.Capabilities == nil {
		http.Error(w, "capabilities not configured", http.StatusServiceUnavailable)
		return
	}
	var req CapabilitiesPatch
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	cur := s.opts.Capabilities.Sharing()
	if req.ShareLocal != nil {
		cur.ShareLocal = clampInt(*req.ShareLocal, 0, 2)
	}
	if req.FileHits != nil {
		cur.FileHits = *req.FileHits
	}
	if req.ContentHits != nil {
		cur.ContentHits = *req.ContentHits
	}
	s.opts.Capabilities.SetSharing(cur)
	s.handleGetCapabilities(w, r)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
