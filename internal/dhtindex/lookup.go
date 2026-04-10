package dhtindex

import (
	"context"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"
)

// Lookup is the read side of Layer D. It holds a set of known
// "indexer" public keys plus a Getter, and runs parallel BEP-44
// gets against (pubkey, keyword) for every known indexer when the
// CLI/HTTP API issues a query.
//
// The set of known indexers can grow over time:
//
//   - Hardcoded seeds: a small list shipped with the client. Empty
//     in M4e; M5+ adds the project's first published seeds.
//   - Gossip-discovered: M4f or later wires the swarmsearch
//     protocol's "peer is sn_search-capable" event into AddIndexer
//     so that any peer we successfully exchange a Layer-S message
//     with becomes a known indexer for Layer D as well.
//   - User-supplied: a CLI command or config file can add pubkeys
//     by hex string.
//
// Lookup is safe for concurrent use.
type Lookup struct {
	getter Getter

	mu       sync.RWMutex
	indexers map[[32]byte]IndexerInfo
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
// It mirrors the wire schema (KeywordHit) but adds Sources — the
// list of indexer pubkey hex strings that returned this hit.
type LookupHit struct {
	InfoHash string // 40-char lowercase hex
	Name     string
	Seeders  int
	Size     int64
	Files    int
	Sources  []string
}

// LookupResponse is the result of Lookup.Query.
type LookupResponse struct {
	// IndexersAsked is the number of indexers we issued a Get to.
	IndexersAsked int
	// IndexersResponded is the number that returned a non-empty
	// non-error result.
	IndexersResponded int
	// Hits is the merged hit list, sorted by source-count descending
	// then by name.
	Hits []LookupHit
}

// NewLookup constructs an empty Lookup wrapped around the given
// Getter. The set of known indexers starts empty; call AddIndexer
// to populate it.
func NewLookup(getter Getter) *Lookup {
	return &Lookup{
		getter:   getter,
		indexers: make(map[[32]byte]IndexerInfo),
	}
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
// Each Get is timeouted by ctx; the per-indexer error is logged at
// debug level on the underlying logger but does not fail the
// overall lookup. As long as at least one indexer returns a
// non-empty value, the response carries that data.
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
	l.mu.RUnlock()

	if len(indexers) == 0 {
		return &LookupResponse{}, nil
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
	resp := &LookupResponse{IndexersAsked: len(indexers)}
	for r := range results {
		if r.err != nil {
			continue
		}
		resp.IndexersResponded++
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
		}
	}

	for _, lh := range merged {
		resp.Hits = append(resp.Hits, *lh)
	}
	sort.Slice(resp.Hits, func(i, j int) bool {
		if len(resp.Hits[i].Sources) != len(resp.Hits[j].Sources) {
			return len(resp.Hits[i].Sources) > len(resp.Hits[j].Sources)
		}
		return resp.Hits[i].Name < resp.Hits[j].Name
	})
	return resp, nil
}
