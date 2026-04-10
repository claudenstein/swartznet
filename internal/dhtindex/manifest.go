package dhtindex

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Manifest is the persistent record of every (publisher, keyword)
// entry this node has published, plus the hits that live inside it.
// It exists so a fresh process can:
//
//   - resume publishing without losing the old hit list (BEP-44
//     does not let you incrementally update; we hold the full hit
//     list locally and re-publish the whole value on every change),
//   - run a periodic refresh ticker (every BEP-44 entry expires
//     after 2h without re-announcement, so we re-publish hourly),
//   - report status to the user via `swartznet publish status`
//     in M4f.
//
// The on-disk form is JSON; we accept the size overhead in exchange
// for forward-compatible reads if the schema picks up new fields.
type Manifest struct {
	mu sync.Mutex

	// path is the on-disk path; empty for in-memory test manifests.
	path string

	// Entries is the keyword → ManifestEntry map. Exposed (lower-
	// case keys) for direct test inspection.
	Entries map[string]*ManifestEntry `json:"entries"`
}

// ManifestEntry is one (publisher, keyword) record. The Hits slice
// is the full set we last published; the publisher rewrites it
// in-memory and re-publishes when a torrent is added or removed.
type ManifestEntry struct {
	Hits          []KeywordHit `json:"hits"`
	LastPublished time.Time    `json:"last_published"`
	LastError     string       `json:"last_error,omitempty"`
	PublishCount  int          `json:"publish_count"`
}

// LoadOrCreateManifest reads a manifest from disk if it exists,
// otherwise returns an empty manifest bound to the same path so the
// next Save persists to that location. Pass an empty path for an
// in-memory test manifest.
func LoadOrCreateManifest(path string) (*Manifest, error) {
	m := &Manifest{path: path, Entries: make(map[string]*ManifestEntry)}
	if path == "" {
		return m, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("dhtindex: mkdir manifest dir: %w", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return m, nil
		}
		return nil, fmt.Errorf("dhtindex: read manifest: %w", err)
	}
	if err := json.Unmarshal(raw, m); err != nil {
		return nil, fmt.Errorf("dhtindex: parse manifest: %w", err)
	}
	if m.Entries == nil {
		m.Entries = make(map[string]*ManifestEntry)
	}
	m.path = path
	return m, nil
}

// Save serialises the manifest to disk. No-op for in-memory test
// manifests (empty path). Atomic via tempfile + rename.
func (m *Manifest) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.path == "" {
		return nil
	}
	raw, err := json.MarshalIndent(struct {
		Entries map[string]*ManifestEntry `json:"entries"`
	}{Entries: m.Entries}, "", "  ")
	if err != nil {
		return fmt.Errorf("dhtindex: marshal manifest: %w", err)
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("dhtindex: write manifest tmp: %w", err)
	}
	if err := os.Rename(tmp, m.path); err != nil {
		return fmt.Errorf("dhtindex: rename manifest: %w", err)
	}
	return nil
}

// AddHit appends or updates a hit under the given keyword. If the
// infohash is already in the entry's Hits list, the existing entry
// is replaced (so seeder counts and names stay fresh). Returns the
// number of total hits in the entry after the update.
//
// AddHit is responsible for keeping the encoded entry size below
// MaxValueBytes; if adding the hit would push the encoded value
// past the cap, the oldest hit is evicted to make room.
func (m *Manifest) AddHit(keyword string, hit KeywordHit) (totalHits int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if keyword == "" {
		return 0, errors.New("dhtindex: empty keyword")
	}
	entry, ok := m.Entries[keyword]
	if !ok {
		entry = &ManifestEntry{}
		m.Entries[keyword] = entry
	}
	// Replace any existing hit with the same infohash.
	for i, h := range entry.Hits {
		if string(h.IH) == string(hit.IH) {
			entry.Hits[i] = hit
			return len(entry.Hits), nil
		}
	}
	entry.Hits = append(entry.Hits, hit)

	// Eviction loop: drop the oldest hit while the encoded form
	// would exceed the cap.
	for len(entry.Hits) > 0 && EstimateValueSize(KeywordValue{Hits: entry.Hits}) > MaxValueBytes {
		entry.Hits = entry.Hits[1:]
	}
	return len(entry.Hits), nil
}

// RemoveHit drops a hit by infohash. No-op if the keyword or hit is
// absent.
func (m *Manifest) RemoveHit(keyword string, infohash []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.Entries[keyword]
	if !ok {
		return
	}
	out := entry.Hits[:0]
	for _, h := range entry.Hits {
		if string(h.IH) != string(infohash) {
			out = append(out, h)
		}
	}
	entry.Hits = out
}

// Snapshot returns a deep copy of every entry. Used by the
// publisher worker so it can iterate without holding the lock for
// the entire put traversal.
func (m *Manifest) Snapshot() map[string]*ManifestEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]*ManifestEntry, len(m.Entries))
	for k, v := range m.Entries {
		hits := make([]KeywordHit, len(v.Hits))
		copy(hits, v.Hits)
		out[k] = &ManifestEntry{
			Hits:          hits,
			LastPublished: v.LastPublished,
			LastError:     v.LastError,
			PublishCount:  v.PublishCount,
		}
	}
	return out
}

// MarkPublished records that the entry for keyword was successfully
// published at the given time. The publish counter is incremented
// and any prior LastError is cleared.
func (m *Manifest) MarkPublished(keyword string, when time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.Entries[keyword]
	if !ok {
		return
	}
	entry.LastPublished = when
	entry.LastError = ""
	entry.PublishCount++
}

// MarkFailed records that the most recent publish attempt failed.
// PublishCount is not incremented.
func (m *Manifest) MarkFailed(keyword string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.Entries[keyword]
	if !ok {
		return
	}
	if err != nil {
		entry.LastError = err.Error()
	}
}

// Keywords returns a sorted slice of every keyword in the manifest.
// Useful for stable iteration in tests and for `publish status`.
func (m *Manifest) Keywords() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.Entries))
	for k := range m.Entries {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
