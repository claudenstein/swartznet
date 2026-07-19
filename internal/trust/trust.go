// Package trust manages SwartzNet's publisher-trust list: a
// persistent set of ed25519 public keys whose signed `.torrent`
// files the local node treats as implicitly trusted. Trusted
// publishers earn three behavioural boosts, all realized by
// CALLERS consulting IsTrusted — this package only owns the set:
//
//  1. Torrents they sign are auto-confirmed into the known-good
//     Bloom filter as soon as metadata arrives — no waiting for
//     the download to complete. The engine's metadata-arrival
//     hook consults IsTrusted(SignedBy()).
//  2. Search results that include their publications are tagged
//     with a TrustedPublisher flag so the GUI can render them
//     with a gold badge.
//  3. Flags ("this is spam") against their content are exempt
//     from reputation demotion. That exemption is NOT enforced
//     here: the shared Flag path filters attributed pubkeys
//     through IsTrusted BEFORE RecordFlagged, so a flag is a
//     no-op against a trusted publisher.
//
// The list is stored as a pretty-printed JSON array of
// {"pubkey","label"} objects (json.MarshalIndent, 2-space
// indent), loaded at daemon startup and mutated through the
// Store API; every mutation is persisted atomically via
// tempfile + rename at mode 0600.
package trust

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Entry is one element of the trust list.
type Entry struct {
	// PubKeyHex is the 64-char lowercase hex form of a 32-byte
	// ed25519 public key. This is the primary key.
	PubKeyHex string `json:"pubkey"`
	// Label is an optional human-readable name for the
	// publisher; omitted from JSON when empty.
	Label string `json:"label,omitempty"`
}

// Store is a thread-safe in-memory view of the trust list backed
// by a JSON file. A zero-value Store is unusable; construct via
// LoadOrCreate.
type Store struct {
	path string

	mu      sync.RWMutex
	entries map[string]Entry // key = lowercase PubKeyHex
}

// LoadOrCreate reads the trust list from path. A missing file is
// not an error and yields an empty store bound to path (the next
// mutation persists there). Unreadable files and malformed JSON
// are returned as errors. A path of "" yields an in-memory-only
// store whose save is a no-op.
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
		// Lowercase on load so a hand-edited mixed-case file
		// still resolves via IsTrusted/Label (which lowercase).
		e.PubKeyHex = strings.ToLower(e.PubKeyHex)
		s.entries[e.PubKeyHex] = e
	}
	return s, nil
}

// Add marks a publisher as trusted. Idempotent; an existing
// entry's label is overwritten. Rejects a pubkey that is not
// 64 hex characters. The hex is lowercased before storage.
// Persists on success.
func (s *Store) Add(pubKeyHex, label string) error {
	if !validPubKeyHex(pubKeyHex) {
		return fmt.Errorf("trust: pubkey must be 64 hex characters, got %d", len(pubKeyHex))
	}
	key := strings.ToLower(pubKeyHex)
	s.mu.Lock()
	s.entries[key] = Entry{PubKeyHex: key, Label: label}
	s.mu.Unlock()
	return s.save()
}

// Remove deletes a publisher from the trust list. Idempotent and
// case-insensitive. Persists on completion.
func (s *Store) Remove(pubKeyHex string) error {
	key := strings.ToLower(pubKeyHex)
	s.mu.Lock()
	delete(s.entries, key)
	s.mu.Unlock()
	return s.save()
}

// IsTrusted reports whether pubKeyHex is in the trust list.
// Read-lock-only hot path; case-insensitive. This is the method
// the flag path and engine auto-confirm consult.
func (s *Store) IsTrusted(pubKeyHex string) bool {
	key := strings.ToLower(pubKeyHex)
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.entries[key]
	return ok
}

// Label returns the stored label for a trusted pubkey, or "" if
// the pubkey is not trusted. Case-insensitive.
func (s *Store) Label(pubKeyHex string) string {
	key := strings.ToLower(pubKeyHex)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if e, ok := s.entries[key]; ok {
		return e.Label
	}
	return ""
}

// List returns every entry sorted ascending by lowercase pubkey
// hex. The returned slice is a fresh copy; mutating it does not
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

// save atomically rewrites the trust file (tempfile + rename at
// mode 0600) from a List() snapshot, so the write lock is not
// held during file I/O. No-op when path is empty. On rename
// failure the tempfile is removed so none leaks.
func (s *Store) save() error {
	if s.path == "" {
		return nil
	}
	list := s.List()
	body, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("trust: marshal: %w", err)
	}
	// Create the parent dir: `swartznet trust add` is an offline command
	// that may run on a clean install before the daemon ever created the
	// XDG data dir.
	dir := filepath.Dir(s.path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("trust: mkdir: %w", err)
		}
	}
	// A UNIQUE tempfile (not a fixed "<path>.tmp") so two concurrent writers
	// never share a tmp inode and tear each other's writes.
	f, err := os.CreateTemp(dir, filepath.Base(s.path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("trust: write tmp: %w", err)
	}
	tmp := f.Name()
	if _, err := f.Write(body); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("trust: write tmp: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("trust: write tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("trust: rename: %w", err)
	}
	return nil
}

// validPubKeyHex reports whether s is exactly 64 hex characters —
// the canonical 32-byte ed25519 public key form.
func validPubKeyHex(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
