package reputation

import (
	"container/list"
	"strings"
	"sync"
)

// SourceTracker remembers, for each recently-seen infohash, the
// set of indexer pubkeys that returned it as a hit. The httpapi
// flag handler reads from it so `swartznet flag <ih>` only
// demotes the indexers that actually claimed the hash, instead
// of falling back to the M5d heuristic of demoting every known
// indexer.
//
// The tracker is bounded with LRU eviction so a long-running
// daemon can not accumulate unbounded memory. Default capacity
// is 10 000 distinct infohashes; entries past that get evicted
// least-recently-used first when a new one is recorded.
//
// Concurrent-safe via a single sync.Mutex. The hot path is the
// O(1) Record / Sources operation, both of which only take the
// lock for a handful of map + list ops.
type SourceTracker struct {
	mu       sync.Mutex
	capacity int
	// items maps infohash hex → *list.Element holding sourceEntry.
	// Same element also lives in the order list for LRU eviction.
	items map[string]*list.Element
	order *list.List
}

// sourceEntry is the value type stored in both the map and the
// LRU list. Pubs is a deduplicated set of indexer pubkeys.
type sourceEntry struct {
	infohash string
	pubs     map[PubKeyHex]struct{}
}

// DefaultSourceCapacity is the LRU bound used when NewSourceTracker
// is called with capacity ≤ 0. 10 000 distinct infohashes at ~80
// bytes each (string key + small set value) is roughly 1 MB.
const DefaultSourceCapacity = 10_000

// NewSourceTracker constructs an empty tracker with the given
// LRU capacity. Pass 0 (or any non-positive value) to use
// DefaultSourceCapacity.
func NewSourceTracker(capacity int) *SourceTracker {
	if capacity <= 0 {
		capacity = DefaultSourceCapacity
	}
	return &SourceTracker{
		capacity: capacity,
		items:    make(map[string]*list.Element, capacity),
		order:    list.New(),
	}
}

// Record adds the given pubkey to the source set for the
// infohash. The infohash is normalised to lowercase hex; an
// empty pubkey or empty infohash is silently ignored.
//
// On insert, the entry is moved to the front of the LRU list. If
// the tracker is at capacity, the oldest entry is evicted.
func (s *SourceTracker) Record(infohash string, pubkey PubKeyHex) {
	if infohash == "" || pubkey == "" {
		return
	}
	key := strings.ToLower(infohash)
	s.mu.Lock()
	defer s.mu.Unlock()

	if elem, ok := s.items[key]; ok {
		entry := elem.Value.(*sourceEntry)
		entry.pubs[pubkey] = struct{}{}
		s.order.MoveToFront(elem)
		return
	}

	entry := &sourceEntry{
		infohash: key,
		pubs:     map[PubKeyHex]struct{}{pubkey: {}},
	}
	elem := s.order.PushFront(entry)
	s.items[key] = elem

	for s.order.Len() > s.capacity {
		oldest := s.order.Back()
		if oldest == nil {
			break
		}
		oldestEntry := oldest.Value.(*sourceEntry)
		s.order.Remove(oldest)
		delete(s.items, oldestEntry.infohash)
	}
}

// RecordMany is a convenience wrapper that records every pubkey
// in the slice for the same infohash. Used by Lookup.Query when
// merging multi-source hits.
func (s *SourceTracker) RecordMany(infohash string, pubkeys []PubKeyHex) {
	for _, p := range pubkeys {
		s.Record(infohash, p)
	}
}

// Sources returns the deduplicated list of pubkeys recorded for
// the infohash, or nil if the entry has never been recorded or
// has been evicted. The returned slice is a fresh copy and is
// safe to iterate without holding the tracker lock. Lookup
// touches the LRU position so a recently-queried infohash stays
// hot.
func (s *SourceTracker) Sources(infohash string) []PubKeyHex {
	if infohash == "" {
		return nil
	}
	key := strings.ToLower(infohash)
	s.mu.Lock()
	defer s.mu.Unlock()

	elem, ok := s.items[key]
	if !ok {
		return nil
	}
	s.order.MoveToFront(elem)
	entry := elem.Value.(*sourceEntry)
	out := make([]PubKeyHex, 0, len(entry.pubs))
	for pk := range entry.pubs {
		out = append(out, pk)
	}
	return out
}

// Forget drops the tracker entry for the given infohash, if any.
// Used after a successful flag so subsequent flags do not double-
// dock the same indexers.
func (s *SourceTracker) Forget(infohash string) {
	if infohash == "" {
		return
	}
	key := strings.ToLower(infohash)
	s.mu.Lock()
	defer s.mu.Unlock()
	elem, ok := s.items[key]
	if !ok {
		return
	}
	s.order.Remove(elem)
	delete(s.items, key)
}

// Len returns the current number of distinct infohashes the
// tracker is holding. Useful for tests and /status diagnostics.
func (s *SourceTracker) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.order.Len()
}
