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

	"github.com/swartznet/swartznet/contracts/dhtschema"
)

// Manifest is the persistent record of every keyword this node publishes, plus
// the hits inside each. BEP-44 has no incremental update, so the full hit list
// is held locally and the whole value is re-published on every change; the
// refresh ticker re-announces before the 2h expiry. The on-disk form is JSON
// (forward-compatible reads over a compact wire).
type Manifest struct {
	mu sync.Mutex

	// path is the on-disk path; empty for in-memory test manifests.
	path string

	// Entries is the keyword → *ManifestEntry map. Exported for test
	// inspection; every present key maps to a non-nil entry post-load.
	Entries map[string]*ManifestEntry `json:"entries"`
}

// ManifestEntry is one keyword's published state.
type ManifestEntry struct {
	Hits          []dhtschema.KeywordHit `json:"hits"`
	LastPublished time.Time              `json:"last_published"`
	LastError     string                 `json:"last_error,omitempty"`
	PublishCount  int                    `json:"publish_count"`
}

// LoadOrCreateManifest reads a manifest if it exists, else returns an empty one
// bound to path so the next Save persists there. An empty path yields an
// in-memory manifest (Save is a no-op).
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
	// Drop JSON-null entries (a hand-edited or truncated file) so the
	// "every key maps to a non-nil entry" invariant holds.
	for k, v := range m.Entries {
		if v == nil {
			delete(m.Entries, k)
		}
	}
	m.path = path
	return m, nil
}

// Save serialises the manifest atomically (tempfile + rename). No-op for an
// in-memory manifest.
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
		_ = os.Remove(tmp)
		return fmt.Errorf("dhtindex: rename manifest: %w", err)
	}
	return nil
}

// AddHit appends or updates a hit under keyword. A hit with a matching
// infohash is replaced (so seeders/name stay fresh). AddHit keeps the encoded
// entry under MaxValueBytes by evicting the OLDEST hit while the estimated
// encoded size would exceed the cap — a replacement can be larger than what it
// displaced, so eviction runs even without a net count change. Returns the hit
// count after the update.
func (m *Manifest) AddHit(keyword string, hit dhtschema.KeywordHit) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if keyword == "" {
		return 0, errors.New("dhtindex: empty keyword")
	}
	entry, ok := m.Entries[keyword]
	if !ok || entry == nil {
		entry = &ManifestEntry{}
		m.Entries[keyword] = entry
	}
	replaced := false
	for i, h := range entry.Hits {
		if string(h.IH) == string(hit.IH) {
			entry.Hits[i] = hit
			replaced = true
			break
		}
	}
	if !replaced {
		entry.Hits = append(entry.Hits, hit)
	}
	for len(entry.Hits) > 0 && dhtschema.EstimateValueSize(dhtschema.KeywordValue{Hits: entry.Hits}) > dhtschema.MaxValueBytes {
		entry.Hits = entry.Hits[1:]
	}
	return len(entry.Hits), nil
}

// RemoveAllHits scrubs infohash from every keyword. Emptied entries are dropped
// so refresh never re-announces an empty value and the manifest stays bounded
// for a long-running publisher. Returns the number of keyword entries touched.
func (m *Manifest) RemoveAllHits(infohash []byte) int {
	if len(infohash) == 0 {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	touched := 0
	for keyword, entry := range m.Entries {
		if entry == nil {
			delete(m.Entries, keyword)
			continue
		}
		hadIt := false
		out := entry.Hits[:0]
		for _, h := range entry.Hits {
			if string(h.IH) == string(infohash) {
				hadIt = true
				continue
			}
			out = append(out, h)
		}
		entry.Hits = out
		if hadIt {
			touched++
		}
		if len(entry.Hits) == 0 {
			delete(m.Entries, keyword)
		}
	}
	return touched
}

// Snapshot returns a deep copy of every entry so the publisher worker can
// iterate without holding the lock across a put traversal.
func (m *Manifest) Snapshot() map[string]*ManifestEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]*ManifestEntry, len(m.Entries))
	for k, v := range m.Entries {
		if v == nil {
			continue
		}
		hits := make([]dhtschema.KeywordHit, len(v.Hits))
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

// MarkPublished records a successful publish: sets LastPublished, clears
// LastError, bumps PublishCount.
func (m *Manifest) MarkPublished(keyword string, when time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if entry, ok := m.Entries[keyword]; ok && entry != nil {
		entry.LastPublished = when
		entry.LastError = ""
		entry.PublishCount++
	}
}

// MarkFailed records the most recent publish failure without bumping
// PublishCount (so a stuck keyword does not look "published").
func (m *Manifest) MarkFailed(keyword string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if entry, ok := m.Entries[keyword]; ok && entry != nil && err != nil {
		entry.LastError = err.Error()
	}
}

// Keywords returns a sorted slice of every keyword. Stable iteration for tests
// and status.
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
