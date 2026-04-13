// Package trust manages SwartzNet's publisher-trust list: a
// persistent set of ed25519 public keys whose signed `.torrent`
// files the local node treats as implicitly trusted. Trusted
// publishers get three behavioural boosts:
//
//  1. Torrents they sign are auto-confirmed into the known-good
//     Bloom filter as soon as metadata arrives — no waiting for
//     the download to complete.
//  2. Search results that include their publications are tagged
//     with a `TrustedPublisher` flag so the GUI can render them
//     with a gold badge.
//  3. Flags ("this is spam") from the user against their content
//     are logged but *not* automatically demoted, since the trust
//     relationship is deliberate.
//
// The trust list is stored as a JSON file, one entry per line in
// a conventional `[{pubkey: "...", label: "..."}]` array. It's
// loaded at daemon startup and mutated through the `Store` API;
// mutations are persisted atomically via tempfile + rename.
package trust

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
)

// Entry is one row in the trust list.
type Entry struct {
	// PubKeyHex is the 64-char lowercase hex form of a 32-byte
	// ed25519 public key. This is the primary key.
	PubKeyHex string `json:"pubkey"`
	// Label is an optional human-readable name for the
	// publisher ("Alice's indexer", "public library mirror").
	Label string `json:"label,omitempty"`
}

// Store is a thread-safe in-memory cache of the trust list
// backed by a JSON file. A zero-value Store is NOT usable;
// always construct via LoadOrCreate.
type Store struct {
	path string

	mu      sync.RWMutex
	entries map[string]Entry // key = PubKeyHex
}

// LoadOrCreate reads the trust list from path, creating an
// empty file if none exists. Errors are returned for unreadable
// files or malformed JSON; a genuinely missing file is not an
// error and yields an empty store.
//
// A path of "" returns an in-memory-only store whose Save method
// is a no-op — useful for tests and ephemeral engines.
func LoadOrCreate(path string) (*Store, error) {
	s := &Store{
		path:    path,
		entries: make(map[string]Entry),
	}
	if path == "" {
		return s, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, fmt.Errorf("trust: open %s: %w", path, err)
	}
	defer f.Close()

	var raw []Entry
	if err := json.NewDecoder(f).Decode(&raw); err != nil {
		return nil, fmt.Errorf("trust: decode %s: %w", path, err)
	}
	for _, e := range raw {
		if !validPubKeyHex(e.PubKeyHex) {
			continue
		}
		s.entries[e.PubKeyHex] = e
	}
	return s, nil
}

// Add marks a publisher as trusted. Idempotent; an existing
// entry's label is overwritten. Returns an error for malformed
// pubkeys. Persists to disk on success.
func (s *Store) Add(pubKeyHex, label string) error {
	if !validPubKeyHex(pubKeyHex) {
		return fmt.Errorf("trust: pubkey must be 64 hex characters, got %d", len(pubKeyHex))
	}
	s.mu.Lock()
	s.entries[pubKeyHex] = Entry{PubKeyHex: pubKeyHex, Label: label}
	s.mu.Unlock()
	return s.save()
}

// Remove deletes a publisher from the trust list. Idempotent.
func (s *Store) Remove(pubKeyHex string) error {
	s.mu.Lock()
	delete(s.entries, pubKeyHex)
	s.mu.Unlock()
	return s.save()
}

// IsTrusted reports whether the given publisher pubkey is in the
// trust list. Safe for hot-path use — only takes a read lock.
func (s *Store) IsTrusted(pubKeyHex string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.entries[pubKeyHex]
	return ok
}

// Label returns the stored label for a trusted pubkey, or empty
// string if not trusted.
func (s *Store) Label(pubKeyHex string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if e, ok := s.entries[pubKeyHex]; ok {
		return e.Label
	}
	return ""
}

// List returns every entry in stable (sorted-by-pubkey) order.
// The returned slice is a fresh copy; mutating it does not
// affect the store.
func (s *Store) List() []Entry {
	s.mu.RLock()
	out := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, e)
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		return out[i].PubKeyHex < out[j].PubKeyHex
	})
	return out
}

// save writes the current entries to disk atomically (tempfile
// + rename). No-op when path is empty.
func (s *Store) save() error {
	if s.path == "" {
		return nil
	}
	list := s.List()
	body, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("trust: marshal: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return fmt.Errorf("trust: write tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("trust: rename: %w", err)
	}
	return nil
}

// validPubKeyHex reports whether s is a 64-char lowercase hex
// string — the canonical form throughout SwartzNet.
func validPubKeyHex(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
