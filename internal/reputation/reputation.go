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
// table because [32]byte is not JSON-friendly. Every lookup path
// must query the lowercase form.
type PubKeyHex string

// PubKey converts a 32-byte ed25519 public key into the canonical
// lowercase-hex PubKeyHex.
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
	// HitsConfirmed is the number of those hits that the user has
	// explicitly confirmed as good via the confirm path.
	HitsConfirmed int `json:"hits_confirmed"`
	// HitsFlagged is the number of hits explicitly flagged as
	// spam by the user via the flag path.
	HitsFlagged int `json:"hits_flagged"`
	// FirstSeen is when we first added a record for this indexer.
	FirstSeen time.Time `json:"first_seen"`
	// LastUpdated is the most recent counter mutation.
	LastUpdated time.Time `json:"last_updated"`
	// SeededAt is the time at which this pubkey was imported from
	// a curated seed list (see MarkSeeded). Zero value means "not
	// a seed". The seed bonus added to the derived score decays
	// exponentially from this point with SeedHalfLife, so an
	// organically-earned score dominates after a few half-lives.
	SeededAt time.Time `json:"seeded_at,omitempty"`
	// SeedLabel is a human-readable tag for a seed entry, usually
	// "maintainer-alice" or similar. Populated by MarkSeeded and
	// shown in status output.
	SeedLabel string `json:"seed_label,omitempty"`
}

// The scoring constants below are a ranking-compatibility contract:
// changing them changes the relative ordering of results across the
// whole network, so they are frozen pending real-world data.
//
// The derived score in [0,1] is:
//
//   - A brand-new indexer (all counters zero) gets the neutral prior
//     defaultUnknownScore — neither boosted nor demoted.
//   - Otherwise the organic score is the Bayesian-smoothed ratio
//     (max(0, confirmed-flagged) + defaultUnknownScore*smoothingPriorWeight)
//     / (max(1, returned) + smoothingPriorWeight). An indexer with
//     hits but zero confirmations is NOT a fixed discount: it decays
//     from the prior toward 0 as returned volume grows (≈0.417 at
//     returned=1, ≈0.024 at returned=100).
//   - A seeded pubkey adds SeedBonus * 2^(-age/SeedHalfLife) on top.
//   - The result is clamped to [0,1].
const (
	defaultUnknownScore  = 0.5
	smoothingPriorWeight = 5.0 // pretend every indexer has 5 prior neutral hits

	// SeedBonus is the initial score bump a freshly-imported seed
	// pubkey gets on top of its organic (smoothed) score. A brand-
	// new seed with zero traffic therefore starts at
	// defaultUnknownScore + SeedBonus ≈ 0.95, well above any
	// reasonable MinIndexerScore cutoff.
	SeedBonus = 0.45

	// SeedHalfLife is how fast the seed bonus decays. The v1 value
	// is 90 days — after one half-life a seed's bonus is 0.225, and
	// after ~6 months it has effectively converged to its organic
	// score. Bootstrap aggressively, then let organic signals win.
	SeedHalfLife = 90 * 24 * time.Hour
)

// Tracker is a persistent per-pubkey reputation table. Safe for
// concurrent use; Save is atomic via tempfile + rename.
type Tracker struct {
	mu sync.RWMutex

	path string
	// Records is exported (lowercase JSON key "records") so tests
	// and the status handler can iterate without poking at internal
	// fields.
	Records map[PubKeyHex]*Counters `json:"records"`
}

// NewTracker returns an empty in-memory Tracker.
func NewTracker() *Tracker {
	return &Tracker{Records: make(map[PubKeyHex]*Counters)}
}

// LoadOrCreateTracker reads a Tracker from disk if it exists,
// otherwise returns an empty one bound to the same path so the
// next Save persists to that location. Empty path is rejected; a
// missing file is not an error.
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
// for in-memory trackers (empty path). Mode 0600.
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
	// A UNIQUE tempfile (not a fixed "<path>.tmp") so concurrent Save calls —
	// the periodic checkpoint racing a user confirm/flag — never share a tmp
	// inode. Two writers on the same fixed tmp interleave their different-
	// length JSON into an invalid document that rename then publishes.
	f, err := os.CreateTemp(filepath.Dir(t.path), filepath.Base(t.path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("reputation: write tracker: %w", err)
	}
	tmp := f.Name()
	if _, err := f.Write(raw); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("reputation: write tracker: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("reputation: write tracker: %w", err)
	}
	if err := os.Rename(tmp, t.path); err != nil {
		// Clean up so repeated rename failures don't leave a
		// growing collection of *.tmp files next to the tracker.
		_ = os.Remove(tmp)
		return fmt.Errorf("reputation: rename tracker: %w", err)
	}
	return nil
}

// RecordReturned increments the HitsReturned counter for the given
// indexer by n. Non-positive n is a no-op and creates no record.
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

// RecordConfirmed increments HitsConfirmed once per pubkey passed.
// Empty varargs is a no-op; passing a pubkey k times bumps it k
// times (multi-source lookups pass each pubkey once per hit).
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

// RecordFlagged increments HitsFlagged once per pubkey passed.
// Symmetric to RecordConfirmed; empty varargs is a no-op.
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
// function. An all-zero record is the neutral prior; otherwise the
// organic score is Bayesian-smoothed toward the prior, with fewer
// returned hits giving the prior more weight. Seeded pubkeys add an
// exponentially decaying bonus, so a fresh seed starts near 1.0 and
// converges to its organic score over ~6 months.
func scoreOf(r *Counters) float64 {
	var organic float64
	if r.HitsReturned == 0 && r.HitsConfirmed == 0 && r.HitsFlagged == 0 {
		organic = defaultUnknownScore
	} else {
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
		organic = (rawScore*returned + defaultUnknownScore*smoothingPriorWeight) /
			(returned + smoothingPriorWeight)
	}

	// Seed bonus: add SeedBonus * 2^(-age / SeedHalfLife) when the
	// pubkey was imported from a seed list. The bonus starts at
	// SeedBonus and halves every SeedHalfLife. A future SeededAt
	// clamps age to 0 (full bonus).
	if !r.SeededAt.IsZero() {
		age := time.Since(r.SeededAt)
		if age < 0 {
			age = 0
		}
		decay := math.Pow(0.5, float64(age)/float64(SeedHalfLife))
		organic += SeedBonus * decay
	}

	if organic < 0 {
		organic = 0
	}
	if organic > 1 {
		organic = 1
	}
	return organic
}

// Snapshot returns a copy of every record, sorted score-descending
// with a lexical PubKey tiebreak. Suitable for status output.
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

// SnapshotEntry is one row in Snapshot output. Counters is a value
// copy so callers cannot mutate the tracker through it.
type SnapshotEntry struct {
	PubKey   PubKeyHex `json:"pubkey"`
	Counters Counters  `json:"counters"`
	Score    float64   `json:"score"`
}

// Threshold reports whether pk's score is at least cutoff. A cutoff
// that is non-positive or NaN always passes (gating disabled), so a
// zero MinIndexerScore admits everyone.
func (t *Tracker) Threshold(pk PubKeyHex, cutoff float64) bool {
	if cutoff <= 0 || math.IsNaN(cutoff) {
		return true
	}
	return t.Score(pk) >= cutoff
}

// MarkSeeded imports a pubkey from a curated seed list. The record
// is created if missing and its SeededAt / SeedLabel fields are set.
// Re-importing an already-seeded pubkey refreshes SeededAt to now,
// restarting the decay (maintainers re-blessing the list).
func (t *Tracker) MarkSeeded(pk PubKeyHex, label string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.touch(pk)
	r.SeededAt = time.Now()
	r.SeedLabel = label
}

// IsSeeded reports whether pk has a record with a non-zero SeededAt.
func (t *Tracker) IsSeeded(pk PubKeyHex) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	r, ok := t.Records[pk]
	if !ok {
		return false
	}
	return !r.SeededAt.IsZero()
}

// AnySeeded reports whether any pubkey in the slice is seeded.
func (t *Tracker) AnySeeded(pks []PubKeyHex) bool {
	for _, pk := range pks {
		if t.IsSeeded(pk) {
			return true
		}
	}
	return false
}

// SeedListEntry is one row of the JSON seed-list file format.
type SeedListEntry struct {
	PubKey string `json:"pubkey"`
	Label  string `json:"label,omitempty"`
}

// SeedList is the top-level JSON structure loaded by LoadSeedList.
// The version field gates format evolution; only v1 is accepted.
type SeedList struct {
	Version int             `json:"version"`
	Seeds   []SeedListEntry `json:"seeds"`
}

// LoadSeedList reads a seed-list JSON file from path and imports
// every entry via MarkSeeded. Empty path and missing file are not
// errors (fresh install allowed). A non-v1 version rejects the whole
// file. Malformed entries are skipped, each contributing one error;
// valid pubkeys are re-normalized to the canonical lowercase key.
//
// Returns (imported, errors): imported is the count successfully
// added; errors is a per-entry (or whole-file) list of failures.
func (t *Tracker) LoadSeedList(path string) (int, []error) {
	if path == "" {
		return 0, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, []error{fmt.Errorf("reputation: read seed list: %w", err)}
	}
	var list SeedList
	if err := json.Unmarshal(raw, &list); err != nil {
		return 0, []error{fmt.Errorf("reputation: parse seed list: %w", err)}
	}
	if list.Version != 1 {
		return 0, []error{fmt.Errorf("reputation: unsupported seed list version %d (want 1)", list.Version)}
	}
	var (
		imported int
		errs     []error
	)
	for i, e := range list.Seeds {
		raw, err := hex.DecodeString(e.PubKey)
		if err != nil || len(raw) != 32 {
			errs = append(errs, fmt.Errorf("reputation: seed entry %d: bad pubkey %q", i, e.PubKey))
			continue
		}
		// Store under the canonical lowercase-hex key derived from the
		// decoded bytes; seed-list JSON may carry upper/mixed-case hex,
		// but every lookup path queries the lowercase form, so we must
		// normalize here or the seed bonus silently never fires.
		var pk [32]byte
		copy(pk[:], raw)
		t.MarkSeeded(PubKey(pk), e.Label)
		imported++
	}
	return imported, errs
}
