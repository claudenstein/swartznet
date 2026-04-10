package httpapi

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"
)

// CompanionController is the narrow interface the HTTP API needs
// from the companion package's publisher and subscriber workers
// to power the M11e GUI integration. The cmd/swartznet binary
// supplies a small adapter that wraps the running *companion.Publisher
// and *companion.SubscriberWorker.
//
// Defined as a local interface so the httpapi package keeps zero
// dependency on internal/companion — the same adapter pattern as
// the existing TorrentController.
type CompanionController interface {
	// PublisherStatus returns a snapshot of the publisher worker
	// state. Empty PubKeyHex means the publisher was not started
	// (no DHT or no identity).
	PublisherStatus() CompanionPublisherStatus
	// RefreshNow asks the publisher to perform an out-of-band
	// refresh, subject to MinInterval throttling. Returns the
	// "too soon" error verbatim so the GUI can show it.
	RefreshNow() error

	// SubscriberStatus returns a snapshot of every followed
	// publisher with its last sync result.
	SubscriberStatus() []CompanionFollowStatus
	// Follow adds a publisher to the subscriber's follow list and
	// persists the new list to disk. label is a human-readable
	// name shown in the GUI; the pubkey is the unique identifier.
	Follow(pubkey [32]byte, label string) error
	// Unfollow removes a publisher from the subscriber's follow
	// list and persists the new list to disk.
	Unfollow(pubkey [32]byte) error
}

// CompanionPublisherStatus mirrors companion.PublisherStatus but
// is re-declared here so the httpapi package does not import
// internal/companion. The cmd/swartznet adapter copies fields
// across.
type CompanionPublisherStatus struct {
	LastRefresh    time.Time `json:"last_refresh"`
	LastInfoHash   string    `json:"last_infohash"`
	LastError      string    `json:"last_error,omitempty"`
	PublishedCount int       `json:"published_count"`
	PubKeyHex      string    `json:"pubkey_hex,omitempty"`
}

// CompanionFollowStatus is one row in the subscriber view: a
// followed publisher and its most recent sync result.
type CompanionFollowStatus struct {
	PubKeyHex        string    `json:"pubkey_hex"`
	Label            string    `json:"label,omitempty"`
	LastSyncAt       time.Time `json:"last_sync_at,omitempty"`
	LastError        string    `json:"last_error,omitempty"`
	TorrentsImported int       `json:"torrents_imported"`
	ContentImported  int       `json:"content_imported"`
	GeneratedAt      int64     `json:"generated_at,omitempty"`
	PointerInfoHash  string    `json:"pointer_infohash,omitempty"`
}

// CompanionStatusResponse is the body returned from GET /companion.
type CompanionStatusResponse struct {
	Publisher  CompanionPublisherStatus `json:"publisher"`
	Subscriber []CompanionFollowStatus  `json:"subscriber"`
}

// followRequestBody is the JSON shape POST /companion/follow
// expects: {pubkey:"<64-char hex>", label:"<name>"}.
type followRequestBody struct {
	PubKey string `json:"pubkey"`
	Label  string `json:"label,omitempty"`
}

// handleCompanionStatus serves GET /companion.
func (s *Server) handleCompanionStatus(w http.ResponseWriter, _ *http.Request) {
	if s.companion == nil {
		http.Error(w, "companion controller not configured", http.StatusServiceUnavailable)
		return
	}
	resp := CompanionStatusResponse{
		Publisher:  s.companion.PublisherStatus(),
		Subscriber: s.companion.SubscriberStatus(),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleCompanionRefresh serves POST /companion/refresh.
func (s *Server) handleCompanionRefresh(w http.ResponseWriter, _ *http.Request) {
	if s.companion == nil {
		http.Error(w, "companion controller not configured", http.StatusServiceUnavailable)
		return
	}
	if err := s.companion.RefreshNow(); err != nil {
		http.Error(w, err.Error(), http.StatusTooManyRequests)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// handleCompanionFollow serves POST /companion/follow.
func (s *Server) handleCompanionFollow(w http.ResponseWriter, r *http.Request) {
	if s.companion == nil {
		http.Error(w, "companion controller not configured", http.StatusServiceUnavailable)
		return
	}
	var body followRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	pub, err := parseFollowPubKey(body.PubKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.companion.Follow(pub, body.Label); err != nil {
		http.Error(w, "follow: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.log.Info("httpapi.companion_follow", "pubkey", body.PubKey, "label", body.Label)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// handleCompanionUnfollow serves POST /companion/unfollow.
func (s *Server) handleCompanionUnfollow(w http.ResponseWriter, r *http.Request) {
	if s.companion == nil {
		http.Error(w, "companion controller not configured", http.StatusServiceUnavailable)
		return
	}
	var body followRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	pub, err := parseFollowPubKey(body.PubKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.companion.Unfollow(pub); err != nil {
		http.Error(w, "unfollow: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.log.Info("httpapi.companion_unfollow", "pubkey", body.PubKey)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// parseFollowPubKey decodes a 64-char hex string into a [32]byte.
func parseFollowPubKey(s string) ([32]byte, error) {
	var out [32]byte
	if len(s) != 64 {
		return out, errHTTP("pubkey must be 64 hex characters")
	}
	raw, err := hex.DecodeString(s)
	if err != nil {
		return out, errHTTP("pubkey is not valid hex: " + err.Error())
	}
	copy(out[:], raw)
	return out, nil
}

// errHTTP is a tiny helper for building plain-text error values
// that handlers can pass to http.Error verbatim.
type httpErr string

func (e httpErr) Error() string { return string(e) }

func errHTTP(s string) error { return httpErr(s) }
