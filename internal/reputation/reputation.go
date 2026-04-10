package reputation

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// PubKeyHex is the 64-character lowercase hex form of an ed25519
// public key. Used as the persistent map key for the reputation
// table because [32]byte is not JSON-friendly.
type PubKeyHex string

// PubKey converts a 32-byte ed25519 public key into PubKeyHex.
func PubKey(pk [32]byte) PubKeyHex {
	return PubKeyHex(hex.EncodeToString(pk[:]))
}

// Counters is the raw counters tracked per indexer pubkey. The
// score is derived from these on demand rather than stored, so
// future tweaks to the score function don't require migrating the
// on-disk file.
type Counters struct {
	// HitsReturned is the total number of hits this indexer has
	// ever returned in our queries.
	HitsReturned int `json:"hits_returned"`
	// HitsConfirmed is the number of those hits that we (the user)
	// have either downloaded successfully or explicitly confirmed
	// as good via `swartznet confirm`.
	HitsConfirmed int `json:"hits_confirmed"`
	// HitsFlagged is the number of hits explicitly flagged as
	// spam by the user via `swartznet flag`.
	HitsFlagged int `json:"hits_flagged"`
	// FirstSeen is when we first added a record for this indexer.
	FirstSeen time.Time `json:"first_seen"`
	// LastUpdated is the most recent counter mutation. Used by
	// the M5d auto-decay logic (M5+).
	LastUpdated time.Time `json:"last_updated"`
}

// Score is the derived 0-1 reputation value used for ranking. The
// formula is intentionally simple and easy to reason about:
//
//   - A brand-new indexer (no hits seen) gets the neutral score
//     defaultUnknownScore. We are neither boosting nor demoting
//     them.
//   - An indexer with hits but zero confirmations and zero flags
//     gets a slight discount: it has had a chance and we have no
//     positive signal. (defaultUnknownScore * 0.8)
//   - The "real" score is hits_confirmed / hits_returned, with
//     hits_flagged subtracted from the numerator. Negative scores
//     are clamped to 0.
//   - Volume bonus: a score derived from a tiny sample is less
//     trustworthy, so we shrink it toward defaultUnknownScore for
//     small samples (Bayesian smoothing).
const (
	defaultUnknownScore  = 0.5
	smoothingPriorWeight = 5.0 // pretend every indexer has 5 prior neutral hits
)

// Tracker is a persistent per-pubkey reputation table. Safe for
// concurrent use; Save is atomic via tempfile + rename.
type Tracker struct {
	mu sync.RWMutex

	path string
	// Records is exposed (lowercase JSON keys) so tests and the
	// HTTP /status handler can iterate without poking at internal
	// fields.
	Records map[PubKeyHex]*Counters `json:"records"`
}

// NewTracker returns an empty in-memory Tracker.
func NewTracker() *Tracker {
	return &Tracker{Records: make(map[PubKeyHex]*Counters)}
}

// LoadOrCreateTracker reads a Tracker from disk if it exists,
// otherwise returns an empty one bound to the same path so the
// next Save persists to that location.
func LoadOrCreateTracker(path string) (*Tracker, error) {
	if path == "" {
		return nil, errors.New("reputation: empty tracker path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("reputation: mkdir tracker dir: %w", err)
	}
	t := &Tracker{path: path, Records: make(map[PubKeyHex]*Counters)}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return t, nil
		}
		return nil, fmt.Errorf("reputation: read tracker: %w", err)
	}
	if err := json.Unmarshal(raw, t); err != nil {
		return nil, fmt.Errorf("reputation: parse tracker: %w", err)
	}
	if t.Records == nil {
		t.Records = make(map[PubKeyHex]*Counters)
	}
	t.path = path
	return t, nil
}

// Save persists the tracker. Atomic via tempfile + rename. No-op
// for in-memory trackers (empty path).
func (t *Tracker) Save() error {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.path == "" {
		return nil
	}
	raw, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("reputation: marshal tracker: %w", err)
	}
	tmp := t.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("reputation: write tracker: %w", err)
	}
	return os.Rename(tmp, t.path)
}

// RecordReturned increments the HitsReturned counter for the given
// indexer by n. Called by the lookup path every time we receive
// hits from an indexer.
func (t *Tracker) RecordReturned(pk PubKeyHex, n int) {
	if n <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.touch(pk)
	r.HitsReturned += n
	r.LastUpdated = time.Now()
}

// RecordConfirmed increments HitsConfirmed for every indexer that
// returned the given infohash recently. M5d wires this to a
// "torrent download succeeded" event so the boost happens
// automatically.
func (t *Tracker) RecordConfirmed(pks ...PubKeyHex) {
	if len(pks) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, pk := range pks {
		r := t.touch(pk)
		r.HitsConfirmed++
		r.LastUpdated = time.Now()
	}
}

// RecordFlagged increments HitsFlagged for every indexer that
// returned the given infohash. M5d wires this to a `swartznet
// flag` CLI command.
func (t *Tracker) RecordFlagged(pks ...PubKeyHex) {
	if len(pks) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, pk := range pks {
		r := t.touch(pk)
		r.HitsFlagged++
		r.LastUpdated = time.Now()
	}
}

// touch returns the Counters for pk, creating a fresh record if
// none exists. Caller must hold t.mu.
func (t *Tracker) touch(pk PubKeyHex) *Counters {
	r, ok := t.Records[pk]
	if !ok {
		now := time.Now()
		r = &Counters{FirstSeen: now, LastUpdated: now}
		t.Records[pk] = r
	}
	return r
}

// Score returns the derived reputation score for pk in [0, 1].
// Unknown pubkeys return defaultUnknownScore.
func (t *Tracker) Score(pk PubKeyHex) float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	r, ok := t.Records[pk]
	if !ok {
		return defaultUnknownScore
	}
	return scoreOf(r)
}

// scoreOf computes the smoothed score for a Counters record. Pure
// function; called by Score and the lookup path.
func scoreOf(r *Counters) float64 {
	if r.HitsReturned == 0 && r.HitsConfirmed == 0 && r.HitsFlagged == 0 {
		return defaultUnknownScore
	}
	// "Effective" returned count: at least 1 to avoid div-by-zero.
	returned := float64(r.HitsReturned)
	if returned < 1 {
		returned = 1
	}
	good := float64(r.HitsConfirmed) - float64(r.HitsFlagged)
	if good < 0 {
		good = 0
	}
	rawScore := good / returned

	// Bayesian smoothing toward the neutral prior. The fewer
	// returned hits, the more weight the prior gets.
	smoothed := (rawScore*returned + defaultUnknownScore*smoothingPriorWeight) /
		(returned + smoothingPriorWeight)
	if smoothed < 0 {
		smoothed = 0
	}
	if smoothed > 1 {
		smoothed = 1
	}
	return smoothed
}

// Snapshot returns a copy of every record, suitable for status
// output. Records are returned in score-descending order.
func (t *Tracker) Snapshot() []SnapshotEntry {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]SnapshotEntry, 0, len(t.Records))
	for pk, r := range t.Records {
		out = append(out, SnapshotEntry{
			PubKey:   pk,
			Counters: *r,
			Score:    scoreOf(r),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].PubKey < out[j].PubKey
	})
	return out
}

// SnapshotEntry is one row in Snapshot output.
type SnapshotEntry struct {
	PubKey   PubKeyHex `json:"pubkey"`
	Counters Counters  `json:"counters"`
	Score    float64   `json:"score"`
}

// Threshold reports whether the given pubkey's score is at least
// the given cutoff. Used by the lookup path to skip indexers below
// a configurable demotion threshold.
func (t *Tracker) Threshold(pk PubKeyHex, cutoff float64) bool {
	if cutoff <= 0 || math.IsNaN(cutoff) {
		return true
	}
	return t.Score(pk) >= cutoff
}
