package dhtindex

import (
	"context"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/swartznet/swartznet/internal/reputation"
)

// Lookup is the read side of Layer D. It holds a set of known
// "indexer" public keys plus a Getter, and runs parallel BEP-44
// gets against (pubkey, keyword) for every known indexer when the
// CLI/HTTP API issues a query.
//
// As of M5c, Lookup also consults two optional spam-resistance
// helpers:
//
//   - A *reputation.Tracker, used to (a) skip indexers whose
//     historical reputation falls below MinIndexerScore and
//     (b) record HitsReturned for every indexer that answers a
//     query, so the score evolves over time.
//   - A *reputation.BloomFilter of "known-good" infohashes, used to
//     boost results whose infohash the user has already downloaded
//     successfully or explicitly confirmed.
//
// As of M9, Lookup also writes per-hit source attribution into
// an optional *reputation.SourceTracker. The HTTP /flag handler
// then reads from the same tracker so a flag command demotes
// only the indexers that actually returned the flagged hash,
// instead of falling back to "demote every known indexer".
//
// As of P2.3 (Aggregate), Lookup can optionally resolve publisher
// PPMI pointers first and fall back to the legacy per-keyword
// path only for publishers who haven't migrated. Attach a
// PPMIGetter via SetPPMIGetter to enable; when nil, Query behaves
// exactly as before.
//
// All helpers are nil by default; the M4e tests still pass
// unchanged. M5d / M9 / P2.3 wire them up via the Engine.
//
// Lookup is safe for concurrent use.
type Lookup struct {
	getter Getter

	mu         sync.RWMutex
	indexers   map[[32]byte]IndexerInfo
	tracker    *reputation.Tracker
	bloom      *reputation.BloomFilter
	sources    *reputation.SourceTracker
	minScore   float64 // skip indexers with score below this
	ppmiGetter PPMIGetter
}

// IndexerInfo is the metadata Lookup tracks per known indexer
// pubkey. Reputation fields land in M5; today we just record when
// the pubkey was added and what its display label is.
type IndexerInfo struct {
	PubKey  [32]byte
	Label   string // optional human-readable name (e.g. "official-seed-1")
	AddedAt time.Time
}

// LookupHit is a single deduplicated result from a Layer-D lookup.
// It mirrors the wire schema (KeywordHit) but adds:
//
//   - Sources: the list of indexer labels that returned this hit
//     (so the UI can show "this hit was reported by 3 indexers");
//   - Score: a 0-1 quality score derived from per-indexer
//     reputation, source count, and Bloom filter membership.
//     Higher is better.
//   - BloomHit: true if the infohash is in the user's known-good
//     Bloom filter. Always false when no filter is wired in.
type LookupHit struct {
	InfoHash string // 40-char lowercase hex
	Name     string
	Seeders  int
	Size     int64
	Files    int
	Sources  []string
	Score    float64
	BloomHit bool
}

// LookupResponse is the result of Lookup.Query.
type LookupResponse struct {
	// IndexersAsked is the number of indexers we issued a Get to.
	IndexersAsked int
	// IndexersResponded is the number that returned a non-empty
	// non-error result.
	IndexersResponded int
	// Hits is the merged hit list, sorted by source-count descending
	// then by name. Populated from the legacy per-keyword path.
	Hits []LookupHit

	// PPMIsResolved is the list of publisher pointers that the
	// Aggregate path successfully fetched. Each entry points at a
	// companion index torrent the caller must download (via the
	// engine) to resolve into actual hits. Empty when no
	// PPMIGetter is attached.
	PPMIsResolved []ResolvedPPMI

	// PPMIMissing counts the indexers for whom PPMI resolution
	// failed (and that therefore fell back to the legacy path).
	// Useful for telemetry / UI "most of your indexers haven't
	// migrated to PPMI yet" warnings.
	PPMIMissing int
}

// ResolvedPPMI is one publisher's PPMI pointer after successful
// DHT fetch. The caller feeds Value.IH to its torrent engine to
// download the companion index, then uses the returned tree's
// prefix-query walker (companion.BTreeReader) to convert this
// pointer into hits for its local search.
type ResolvedPPMI struct {
	PubKey [32]byte
	Label  string
	Value  PPMIValue
}

// NewLookup constructs an empty Lookup wrapped around the given
// Getter. The set of known indexers starts empty; call AddIndexer
// to populate it. Reputation tracker and Bloom filter are nil by
// default; call SetTracker / SetBloom to wire them in.
func NewLookup(getter Getter) *Lookup {
	return &Lookup{
		getter:   getter,
		indexers: make(map[[32]byte]IndexerInfo),
	}
}

// SetTracker attaches (or detaches) a reputation tracker. When
// non-nil, Query skips indexers whose score is below MinScore and
// records HitsReturned for every indexer that responds.
func (l *Lookup) SetTracker(t *reputation.Tracker) {
	l.mu.Lock()
	l.tracker = t
	l.mu.Unlock()
}

// SetBloom attaches (or detaches) a known-good Bloom filter. When
// non-nil, Query marks each LookupHit with BloomHit=true and adds
// the corresponding boost to Score.
func (l *Lookup) SetBloom(b *reputation.BloomFilter) {
	l.mu.Lock()
	l.bloom = b
	l.mu.Unlock()
}

// SetSourceTracker attaches a per-hit source tracker (M9). When
// non-nil, Query records every (infohash → indexer pubkey)
// mapping it observes, so a later flag command can demote only
// the specific indexers that returned the flagged hash.
func (l *Lookup) SetSourceTracker(s *reputation.SourceTracker) {
	l.mu.Lock()
	l.sources = s
	l.mu.Unlock()
}

// SetMinIndexerScore configures the minimum reputation score an
// indexer must have to be queried. Zero (the default) means no
// filtering. Has no effect when no Tracker is attached.
func (l *Lookup) SetMinIndexerScore(s float64) {
	l.mu.Lock()
	l.minScore = s
	l.mu.Unlock()
}

// SetPPMIGetter attaches (or detaches) a PPMI resolver. When
// non-nil, Query first fans out PPMI gets to all known indexers
// and reports successful pointers via LookupResponse.PPMIsResolved.
// Publishers for whom PPMI fetch fails (most likely: they haven't
// migrated yet) fall through to the legacy per-keyword path
// unchanged. When nil, Query behaves exactly as it did before
// Aggregate landed.
func (l *Lookup) SetPPMIGetter(g PPMIGetter) {
	l.mu.Lock()
	l.ppmiGetter = g
	l.mu.Unlock()
}

// PPMIGetter returns the attached PPMI getter, or nil.
func (l *Lookup) PPMIGetter() PPMIGetter {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.ppmiGetter
}

// Tracker returns the attached reputation tracker, or nil.
func (l *Lookup) Tracker() *reputation.Tracker {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.tracker
}

// Bloom returns the attached known-good Bloom filter, or nil.
func (l *Lookup) Bloom() *reputation.BloomFilter {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.bloom
}

// AddIndexer records a known indexer pubkey. Idempotent: re-adding
// the same pubkey updates the label but does not bump AddedAt.
func (l *Lookup) AddIndexer(pubkey [32]byte, label string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if existing, ok := l.indexers[pubkey]; ok {
		existing.Label = label
		l.indexers[pubkey] = existing
		return
	}
	l.indexers[pubkey] = IndexerInfo{
		PubKey:  pubkey,
		Label:   label,
		AddedAt: time.Now(),
	}
}

// AddIndexerHex parses a 64-char hex pubkey and calls AddIndexer.
// Returns an error if the hex string is malformed.
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

// RemoveIndexer drops a known indexer. No-op if not present.
func (l *Lookup) RemoveIndexer(pubkey [32]byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.indexers, pubkey)
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

// Query runs a Layer-D keyword lookup. The query is tokenised
// internally; for the first cut we issue a Get against the FIRST
// returned token (which is the longest non-stopword in input order)
// per indexer. Multi-keyword AND/OR queries land in a later
// milestone.
//
// As of M5c the lookup also:
//
//   - Filters indexers below MinIndexerScore via the attached
//     reputation.Tracker (no-op when no tracker is configured).
//   - Records HitsReturned per indexer for every indexer that
//     responded with at least one hit, so the tracker stays
//     current.
//   - Marks each LookupHit's BloomHit field if the infohash is in
//     the user's known-good Bloom filter, and folds that flag plus
//     a per-source reputation average into the LookupHit.Score
//     used for sorting.
func (l *Lookup) Query(ctx context.Context, query string) (*LookupResponse, error) {
	tokens := Tokenize(query)
	if len(tokens) == 0 {
		return nil, errors.New("dhtindex: query produces no tokens")
	}
	keyword := tokens[0]
	salt, err := SaltForKeyword(keyword)
	if err != nil {
		return nil, err
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
	ppmiGetter := l.ppmiGetter
	l.mu.RUnlock()

	if len(indexers) == 0 {
		return &LookupResponse{}, nil
	}

	// Apply the reputation cutoff. If a tracker is wired in and
	// MinIndexerScore is non-zero, skip every indexer whose
	// historical score is below the threshold. Empty result after
	// filtering is still a valid response.
	if tracker != nil && minScore > 0 {
		filtered := indexers[:0]
		for _, info := range indexers {
			if tracker.Threshold(reputation.PubKey(info.PubKey), minScore) {
				filtered = append(filtered, info)
			}
		}
		indexers = filtered
	}

	// Aggregate path: fan out PPMI resolution first when enabled.
	// Publishers with a live PPMI are removed from the legacy
	// fallback set — we trust the Aggregate pointer and don't
	// double-query their per-keyword items.
	var ppmis []ResolvedPPMI
	var ppmiMissing int
	resolvedMask := make(map[[32]byte]bool)
	if ppmiGetter != nil {
		ppmis, ppmiMissing = resolvePPMIs(ctx, ppmiGetter, indexers)
		for _, p := range ppmis {
			resolvedMask[p.PubKey] = true
		}
	}

	fallback := indexers[:0:len(indexers)]
	for _, info := range indexers {
		if !resolvedMask[info.PubKey] {
			fallback = append(fallback, info)
		}
	}

	resp := l.legacyQuery(ctx, salt, fallback, tracker, bloom, sources)
	resp.IndexersAsked = len(indexers) // PPMI + legacy fan-out together
	resp.PPMIsResolved = ppmis
	resp.PPMIMissing = ppmiMissing
	return resp, nil
}

// resolvePPMIs fans out PPMI gets to every indexer and returns
// the list of successful resolutions plus a count of failures.
func resolvePPMIs(ctx context.Context, getter PPMIGetter, indexers []IndexerInfo) ([]ResolvedPPMI, int) {
	type ppmiResult struct {
		info IndexerInfo
		v    PPMIValue
		err  error
	}
	results := make(chan ppmiResult, len(indexers))
	var wg sync.WaitGroup
	for _, info := range indexers {
		wg.Add(1)
		go func(info IndexerInfo) {
			defer wg.Done()
			v, err := getter.GetPPMI(ctx, info.PubKey)
			results <- ppmiResult{info: info, v: v, err: err}
		}(info)
	}
	wg.Wait()
	close(results)

	var ppmis []ResolvedPPMI
	missing := 0
	for r := range results {
		if r.err != nil {
			missing++
			continue
		}
		ppmis = append(ppmis, ResolvedPPMI{
			PubKey: r.info.PubKey,
			Label:  r.info.Label,
			Value:  r.v,
		})
	}
	return ppmis, missing
}

// legacyQuery performs the pre-Aggregate per-keyword fan-out for
// a set of indexers. Extracted from Query so the new
// Query-with-PPMI path can short-circuit for publishers already
// resolved via Aggregate. When the indexer set is empty, returns
// a zeroed response.
func (l *Lookup) legacyQuery(ctx context.Context, salt []byte, indexers []IndexerInfo, tracker *reputation.Tracker, bloom *reputation.BloomFilter, sources *reputation.SourceTracker) *LookupResponse {
	resp := &LookupResponse{IndexersAsked: len(indexers)}
	if len(indexers) == 0 {
		return resp
	}

	type fetchResult struct {
		info IndexerInfo
		v    KeywordValue
		err  error
	}
	results := make(chan fetchResult, len(indexers))
	var wg sync.WaitGroup
	for _, info := range indexers {
		wg.Add(1)
		go func(info IndexerInfo) {
			defer wg.Done()
			val, err := l.getter.Get(ctx, info.PubKey, salt)
			results <- fetchResult{info: info, v: val, err: err}
		}(info)
	}
	wg.Wait()
	close(results)

	merged := make(map[string]*LookupHit)
	hitSources := make(map[string][][32]byte)
	for r := range results {
		if r.err != nil {
			continue
		}
		resp.IndexersResponded++
		if tracker != nil {
			tracker.RecordReturned(reputation.PubKey(r.info.PubKey), len(r.v.Hits))
		}
		labelOrHex := r.info.Label
		if labelOrHex == "" {
			labelOrHex = hex.EncodeToString(r.info.PubKey[:])[:16]
		}
		for _, h := range r.v.Hits {
			ih := hex.EncodeToString(h.IH)
			if len(ih) != 40 {
				continue
			}
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
			lh.Sources = append(lh.Sources, labelOrHex)
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
			pkHexes := make([]reputation.PubKeyHex, 0, len(pks))
			for _, pk := range pks {
				pkHexes = append(pkHexes, reputation.PubKey(pk))
			}
			sources.RecordMany(ih, pkHexes)
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
	return resp
}

// scoreLookupHit computes the LookupHit.Score in [0, 1].
//
// The base score is the average reputation of every indexer that
// returned this hit (or 0.5 when no tracker is attached). Hits
// reported by multiple indexers get a small additive bonus per
// extra source (capped). Bloom-filter hits get a flat boost.
//
// The exact weights are easy to tune later; what matters today is
// that confirmed-good infohashes from high-reputation indexers
// always sort above unknown-quality ones.
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
	avg := sum / float64(len(sources))
	// Multi-source bonus: each extra source above the first adds
	// 0.05, capped at 0.2 (i.e. 4 or more sources gets the full
	// bonus). The bonus tops out before it can dominate the
	// underlying reputation signal.
	bonus := 0.05 * float64(len(sources)-1)
	if bonus > 0.2 {
		bonus = 0.2
	}
	score := avg + bonus
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
