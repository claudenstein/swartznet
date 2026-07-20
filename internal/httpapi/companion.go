package httpapi

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// CompanionController is the companion pub/sub surface (Slice 10). httpapi
// declares it locally so it never imports internal/companion; the daemon adapts.
type CompanionController interface {
	PublisherStatus() CompanionPublisherStatus
	RefreshNow() error
	SubscriberStatus() []CompanionFollowStatus
	Follow(pubkey [32]byte, label string) error
	Unfollow(pubkey [32]byte) error
}

// ErrCompanionUnavailable signals that a companion operation cannot proceed
// because the relevant leg (publisher or subscriber) is not wired on this node
// — e.g. when the DHT is disabled. Handlers map it to 503 (feature unavailable),
// distinct from 500 (a real failure of a wired subsystem), so a client can
// disable the control instead of surfacing it as an error. The daemon adapter
// wraps this sentinel when a leg is absent.
var ErrCompanionUnavailable = errors.New("companion feature not available on this node")

// companionErrStatus maps a controller error to an HTTP status: 503 when the
// feature is simply not wired, 500 for a genuine failure.
func companionErrStatus(err error) int {
	if errors.Is(err, ErrCompanionUnavailable) {
		return http.StatusServiceUnavailable
	}
	return http.StatusInternalServerError
}

// CompanionPublisherStatus reports the companion publisher.
type CompanionPublisherStatus struct {
	LastRefresh    time.Time `json:"last_refresh"`
	LastInfoHash   string    `json:"last_infohash,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
	PublishedCount int       `json:"published_count"`
	PubKeyHex      string    `json:"pubkey_hex,omitempty"` // empty ⇒ publisher not started
}

// CompanionFollowStatus reports one followed publisher's last sync.
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

// CompanionStatusResponse is the GET /companion document.
type CompanionStatusResponse struct {
	Publisher  CompanionPublisherStatus `json:"publisher"`
	Subscriber []CompanionFollowStatus  `json:"subscriber"`
}

// followRequestBody is the POST /companion/follow & /unfollow body.
type followRequestBody struct {
	PubKey string `json:"pubkey"`
	Label  string `json:"label,omitempty"`
}

func (s *Server) companionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /companion", s.handleCompanionStatus)
	mux.HandleFunc("POST /companion/refresh", s.handleCompanionRefresh)
	mux.HandleFunc("POST /companion/follow", s.handleCompanionFollow)
	mux.HandleFunc("POST /companion/unfollow", s.handleCompanionUnfollow)
}

func (s *Server) handleCompanionStatus(w http.ResponseWriter, _ *http.Request) {
	if s.opts.Companion == nil {
		http.Error(w, "companion controller not configured", http.StatusServiceUnavailable)
		return
	}
	subs := s.opts.Companion.SubscriberStatus()
	if subs == nil {
		subs = []CompanionFollowStatus{}
	}
	writeJSON(w, CompanionStatusResponse{
		Publisher:  s.opts.Companion.PublisherStatus(),
		Subscriber: subs,
	})
}

func (s *Server) handleCompanionRefresh(w http.ResponseWriter, _ *http.Request) {
	if s.opts.Companion == nil {
		http.Error(w, "companion controller not configured", http.StatusServiceUnavailable)
		return
	}
	if err := s.opts.Companion.RefreshNow(); err != nil {
		if errors.Is(err, ErrCompanionUnavailable) {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		// Otherwise the refresh was throttled (ErrTooSoon), the common case.
		http.Error(w, err.Error(), http.StatusTooManyRequests)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleCompanionFollow(w http.ResponseWriter, r *http.Request) {
	if s.opts.Companion == nil {
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
	if err := s.opts.Companion.Follow(pub, body.Label); err != nil {
		http.Error(w, "follow: "+err.Error(), companionErrStatus(err))
		return
	}
	s.log.Info("httpapi.companion_follow", "pubkey", body.PubKey, "label", body.Label)
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleCompanionUnfollow(w http.ResponseWriter, r *http.Request) {
	if s.opts.Companion == nil {
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
	if err := s.opts.Companion.Unfollow(pub); err != nil {
		http.Error(w, "unfollow: "+err.Error(), companionErrStatus(err))
		return
	}
	s.log.Info("httpapi.companion_unfollow", "pubkey", body.PubKey)
	writeJSON(w, map[string]bool{"ok": true})
}

// parseFollowPubKey validates a 64-hex publisher pubkey.
func parseFollowPubKey(s string) ([32]byte, error) {
	var out [32]byte
	if len(s) != 64 {
		return out, errors.New("pubkey must be 64 hex characters")
	}
	raw, err := hex.DecodeString(s)
	if err != nil {
		return out, errors.New("pubkey is not valid hex: " + err.Error())
	}
	copy(out[:], raw)
	return out, nil
}
