package reputation

import (
	"container/list"
	"strings"
	"sync"
)

// SourceTracker remembers, for each recently-seen infohash, the set
// of indexer pubkeys that returned it as a hit. The flag path reads
// from it so flagging an infohash only demotes the indexers that
// actually claimed that hash, instead of a blanket "demote every
// indexer" fallback.
//
// The tracker is bounded with LRU eviction so a long-running daemon
// cannot accumulate unbounded memory. Always in-memory; no disk
// persistence. Concurrent-safe via a single sync.Mutex.
type SourceTracker struct {
	mu       sync.Mutex
	capacity int
	// items maps lowercased infohash hex → *list.Element holding a
	// *sourceEntry. The same element lives in the order list for LRU.
	items map[string]*list.Element
	order *list.List
}

// sourceEntry is the value stored in both the map and the LRU list.
// pubs is a deduplicated set of indexer pubkeys.
type sourceEntry struct {
	infohash string
	pubs     map[PubKeyHex]struct{}
}

// DefaultSourceCapacity is the LRU bound used when NewSourceTracker
// is called with capacity <= 0.
const DefaultSourceCapacity = 10_000

// NewSourceTracker constructs an empty tracker with the given LRU
// capacity. Non-positive capacity uses DefaultSourceCapacity.
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

// Record adds pubkey to the source set for infohash. The infohash
// is normalised to lowercase hex; an empty infohash or empty pubkey
// is silently ignored. The entry is moved to the front of the LRU
// list; a new entry over capacity evicts the least-recently-used.
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

// RecordMany records every pubkey in the slice for the same
// infohash. Used when merging multi-source hits during lookup.
func (s *SourceTracker) RecordMany(infohash string, pubkeys []PubKeyHex) {
	for _, p := range pubkeys {
		s.Record(infohash, p)
	}
}

// Sources returns a fresh copy of the deduplicated pubkeys recorded
// for infohash, or nil if never recorded or evicted. The lookup
// touches the LRU position so a recently-queried infohash stays hot.
// The returned slice's order is unspecified (map iteration order).
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

// Forget drops the tracker entry for infohash, if any. Used after a
// successful flag so subsequent flags do not double-dock the same
// indexers. Empty or unknown infohash is a no-op.
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

// Len returns the number of distinct infohashes currently held.
func (s *SourceTracker) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.order.Len()
}
