package dhtindex

import (
	"context"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/swartznet/swartznet/contracts/dhtschema"
	"github.com/swartznet/swartznet/contracts/token"
	"github.com/swartznet/swartznet/internal/reputation"
)

// Lookup is the read side of Layer D: a set of known indexer pubkeys plus a
// RecordBackend. Query derives the MOST-DISTINCTIVE token from the search text,
// fans out backend.Lookup to every (non-filtered) indexer concurrently, and
// merges + scores the results. The token, reputation filter, and scoring live
// ABOVE the backend port; the per-indexer DHT get lives beneath it.
//
// Optional spam-resistance helpers (all nil by default):
//   - *reputation.Tracker skips indexers below MinIndexerScore and records
//     HitsReturned so scores evolve.
//   - *reputation.BloomFilter boosts hits whose infohash the user already
//     downloaded/confirmed.
//   - *reputation.SourceTracker records per-hit (infohash → indexer) so a
//     later /flag demotes only the indexers that returned the flagged hash.
//
// Lookup is safe for concurrent use.
type Lookup struct {
	backend RecordBackend

	mu       sync.RWMutex
	indexers map[[32]byte]IndexerInfo
	tracker  *reputation.Tracker
	bloom    *reputation.BloomFilter
	sources  *reputation.SourceTracker
	minScore float64
}

// IndexerInfo is the metadata Lookup tracks per known indexer pubkey.
type IndexerInfo struct {
	PubKey  [32]byte
	Label   string
	AddedAt time.Time
	// Auto is true for indexers discovered from untrusted gossip
	// (NotePublisherSeen) rather than added explicitly by the operator. The auto
	// set is capped + FIFO-evicted so a peer flooding fresh-keypair sync records
	// cannot grow the lookup set (and per-query DHT fanout) without bound.
	Auto bool
}

// maxAutoIndexers caps the gossip-discovered indexer set. Explicit operator
// indexers (AddIndexer/AddIndexerHex) are exempt and unbounded by operator
// choice; only untrusted auto-discovered pubkeys are capped. The value also
// bounds Query's per-search DHT fanout for the auto set.
const maxAutoIndexers = 128

// LookupHit is a deduplicated Layer-D result across all responding indexers.
type LookupHit struct {
	InfoHash string // 40-hex lowercase
	Name     string
	Seeders  int
	Size     int64
	Files    int
	Sources  []string
	Score    float64 // 0..1, higher better
	BloomHit bool    // in the user's known-good Bloom
}

// LookupResponse is the result of Lookup.Query. (PPMI fields are deliberately
// omitted in this slice — the RecordBackend port, not response-struct fields,
// is the Slice-12 Aggregate forward-compat seam.)
type LookupResponse struct {
	IndexersAsked     int
	IndexersResponded int
	Hits              []LookupHit
}

// NewLookup constructs an empty Lookup over backend. The indexer set starts
// empty; call AddIndexer / AddIndexerHex to populate it.
func NewLookup(backend RecordBackend) *Lookup {
	return &Lookup{backend: backend, indexers: make(map[[32]byte]IndexerInfo)}
}

// SetTracker attaches (or detaches) a reputation tracker.
func (l *Lookup) SetTracker(t *reputation.Tracker) {
	l.mu.Lock()
	l.tracker = t
	l.mu.Unlock()
}

// SetBloom attaches (or detaches) a known-good Bloom filter.
func (l *Lookup) SetBloom(b *reputation.BloomFilter) {
	l.mu.Lock()
	l.bloom = b
	l.mu.Unlock()
}

// SetSourceTracker attaches (or detaches) a per-hit source tracker.
func (l *Lookup) SetSourceTracker(s *reputation.SourceTracker) {
	l.mu.Lock()
	l.sources = s
	l.mu.Unlock()
}

// SetMinIndexerScore configures the minimum reputation an indexer must have to
// be queried. Zero (default) disables filtering; no effect without a tracker.
func (l *Lookup) SetMinIndexerScore(s float64) {
	l.mu.Lock()
	l.minScore = s
	l.mu.Unlock()
}

// AddIndexer records a known indexer pubkey EXPLICITLY (operator-added, exempt
// from the auto-set cap). Idempotent: re-adding updates the label but does not
// bump AddedAt, and promotes a previously auto-discovered pubkey to explicit.
func (l *Lookup) AddIndexer(pubkey [32]byte, label string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.addIndexerLocked(pubkey, label, false)
}

// addIndexerLocked upserts an indexer. auto marks a gossip-discovered entry,
// which is subject to the maxAutoIndexers cap (oldest auto entry evicted first).
// Caller holds l.mu.
func (l *Lookup) addIndexerLocked(pubkey [32]byte, label string, auto bool) {
	if existing, ok := l.indexers[pubkey]; ok {
		if !auto {
			// Explicit (re-)add: original unconditional label update, and promote
			// the entry out of the capped auto set.
			existing.Label = label
			existing.Auto = false
			l.indexers[pubkey] = existing
		}
		// An auto re-note of an existing entry is a no-op (no AddedAt refresh, so
		// re-noting cannot keep a stale entry alive against FIFO eviction).
		return
	}
	if auto {
		l.evictOldestAutoIfFullLocked()
	}
	l.indexers[pubkey] = IndexerInfo{PubKey: pubkey, Label: label, AddedAt: time.Now(), Auto: auto}
}

// evictOldestAutoIfFullLocked drops the oldest auto-discovered indexer when the
// auto set is at capacity, so a new auto entry cannot grow the set past the cap.
// Caller holds l.mu.
func (l *Lookup) evictOldestAutoIfFullLocked() {
	autoCount := 0
	var oldestKey [32]byte
	var oldest time.Time
	found := false
	for k, info := range l.indexers {
		if !info.Auto {
			continue
		}
		autoCount++
		if !found || info.AddedAt.Before(oldest) {
			oldest, oldestKey, found = info.AddedAt, k, true
		}
	}
	if autoCount >= maxAutoIndexers && found {
		delete(l.indexers, oldestKey)
	}
}

// AddIndexerHex parses a 64-hex pubkey and calls AddIndexer.
func (l *Lookup) AddIndexerHex(hexKey, label string) error {
	if len(hexKey) != 64 {
		return errors.New("dhtindex: indexer key must be 64 hex characters")
	}
	raw, err := hex.DecodeString(hexKey)
	if err != nil {
		return err
	}
	var pub [32]byte
	copy(pub[:], raw)
	l.AddIndexer(pub, label)
	return nil
}

// RemoveIndexer drops a known indexer. No-op if absent.
func (l *Lookup) RemoveIndexer(pubkey [32]byte) {
	l.mu.Lock()
	delete(l.indexers, pubkey)
	l.mu.Unlock()
}

// Indexers returns a snapshot of every known indexer.
func (l *Lookup) Indexers() []IndexerInfo {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]IndexerInfo, 0, len(l.indexers))
	for _, info := range l.indexers {
		out = append(out, info)
	}
	return out
}

// NotePublisherSeen satisfies swarmsearch.PublisherObserver: a publisher pubkey
// gossiped in a sync record enters the lookup set automatically — as a capped,
// FIFO-evicted AUTO entry, so an attacker flooding fresh-keypair records cannot
// grow the indexer set (or the per-query DHT fanout) without bound.
func (l *Lookup) NotePublisherSeen(pubkey [32]byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.addIndexerLocked(pubkey, "", true)
}

// Query runs a Layer-D keyword lookup. The keyword is the MOST-DISTINCTIVE
// token of the query (longest by byte length, earliest wins) — NOT the first
// token, so "new ubuntu" resolves the "ubuntu" target. Multi-word AND/OR is a
// single-keyword DHT lookup plus client-side intersection; it is never pushed
// into the DHT. An empty indexer set (or one filtered empty by the reputation
// cutoff) yields an empty response, not an error.
func (l *Lookup) Query(ctx context.Context, query string) (*LookupResponse, error) {
	keyword := token.MostDistinctive(token.Tokenize(query))
	if keyword == "" {
		return nil, errors.New("dhtindex: query produces no tokens")
	}

	l.mu.RLock()
	indexers := make([]IndexerInfo, 0, len(l.indexers))
	for _, info := range l.indexers {
		indexers = append(indexers, info)
	}
	tracker := l.tracker
	bloom := l.bloom
	sources := l.sources
	minScore := l.minScore
	l.mu.RUnlock()

	if tracker != nil && minScore > 0 {
		filtered := indexers[:0]
		for _, info := range indexers {
			if tracker.Threshold(reputation.PubKey(info.PubKey), minScore) {
				filtered = append(filtered, info)
			}
		}
		indexers = filtered
	}
	if len(indexers) == 0 {
		return &LookupResponse{}, nil
	}

	resp := &LookupResponse{IndexersAsked: len(indexers)}

	type fetchResult struct {
		info IndexerInfo
		hits []dhtschema.KeywordHit
		err  error
	}
	results := make(chan fetchResult, len(indexers))
	var wg sync.WaitGroup
	for _, info := range indexers {
		wg.Add(1)
		go func(info IndexerInfo) {
			defer wg.Done()
			hits, err := l.backend.Lookup(ctx, info.PubKey, keyword)
			results <- fetchResult{info: info, hits: hits, err: err}
		}(info)
	}
	wg.Wait()
	close(results)

	merged := make(map[string]*LookupHit)
	hitSources := make(map[string][][32]byte)
	for r := range results {
		if r.err != nil {
			continue // one responder suffices; tolerate per-indexer errors
		}
		resp.IndexersResponded++
		if tracker != nil {
			tracker.RecordReturned(reputation.PubKey(r.info.PubKey), len(r.hits))
		}
		label := r.info.Label
		if label == "" {
			label = hex.EncodeToString(r.info.PubKey[:])[:16]
		}
		// One indexer contributes each infohash to the source count AT MOST
		// ONCE. Without this, a hostile indexer could repeat the same infohash
		// in its own KeywordValue.Hits to fake multi-indexer consensus —
		// inflating both the +0.05/extra-source score bonus and the
		// source-count sort tiebreak, defeating the anti-spam signal those
		// exist for. Honest publishers never emit duplicates (Manifest.AddHit
		// replaces a same-infohash hit), so this only disarms the attack.
		seenFromIndexer := make(map[string]struct{}, len(r.hits))
		for _, h := range r.hits {
			ih := hex.EncodeToString(h.IH)
			if len(ih) != 40 {
				continue
			}
			if _, dup := seenFromIndexer[ih]; dup {
				continue
			}
			seenFromIndexer[ih] = struct{}{}
			lh, ok := merged[ih]
			if !ok {
				lh = &LookupHit{InfoHash: ih, Name: h.N, Seeders: h.S, Size: h.Sz, Files: h.F}
				merged[ih] = lh
			} else {
				if lh.Name == "" && h.N != "" {
					lh.Name = h.N
				}
				if h.S > lh.Seeders {
					lh.Seeders = h.S
				}
				if lh.Size == 0 && h.Sz > 0 {
					lh.Size = h.Sz
				}
				if lh.Files == 0 && h.F > 0 {
					lh.Files = h.F
				}
			}
			lh.Sources = append(lh.Sources, label)
			hitSources[ih] = append(hitSources[ih], r.info.PubKey)
			if bloom != nil && bloom.Test(h.IH) {
				lh.BloomHit = true
			}
		}
	}

	for ih, lh := range merged {
		lh.Score = scoreLookupHit(lh, hitSources[ih], tracker)
	}
	if sources != nil {
		for ih, pks := range hitSources {
			hexes := make([]reputation.PubKeyHex, 0, len(pks))
			for _, pk := range pks {
				hexes = append(hexes, reputation.PubKey(pk))
			}
			sources.RecordMany(ih, hexes)
		}
	}

	for _, lh := range merged {
		resp.Hits = append(resp.Hits, *lh)
	}
	sort.Slice(resp.Hits, func(i, j int) bool {
		if resp.Hits[i].BloomHit != resp.Hits[j].BloomHit {
			return resp.Hits[i].BloomHit
		}
		if resp.Hits[i].Score != resp.Hits[j].Score {
			return resp.Hits[i].Score > resp.Hits[j].Score
		}
		if len(resp.Hits[i].Sources) != len(resp.Hits[j].Sources) {
			return len(resp.Hits[i].Sources) > len(resp.Hits[j].Sources)
		}
		return resp.Hits[i].Name < resp.Hits[j].Name
	})
	return resp, nil
}

// scoreLookupHit computes LookupHit.Score in [0,1]: the mean source reputation
// (0.5 with no tracker), plus 0.05 per extra source (capped at +0.2), plus a
// flat 0.25 Bloom boost, clamped. Confirmed-good hits from high-reputation
// indexers always sort above unknown ones.
func scoreLookupHit(lh *LookupHit, sources [][32]byte, tracker *reputation.Tracker) float64 {
	if len(sources) == 0 {
		return 0
	}
	var sum float64
	for _, pk := range sources {
		if tracker != nil {
			sum += tracker.Score(reputation.PubKey(pk))
		} else {
			sum += 0.5
		}
	}
	score := sum / float64(len(sources))
	if bonus := 0.05 * float64(len(sources)-1); bonus > 0 {
		if bonus > 0.2 {
			bonus = 0.2
		}
		score += bonus
	}
	if lh.BloomHit {
		score += 0.25
	}
	if score > 1 {
		score = 1
	}
	if score < 0 {
		score = 0
	}
	return score
}
