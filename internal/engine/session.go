package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// session is the on-disk record of which torrents the engine had
// open when it was last running, plus per-torrent state (paused,
// indexing, queue order). On daemon startup the engine reads the
// session and re-adds every entry; the underlying anacrolix
// Storage backend detects existing piece data on disk and resumes
// each torrent from where it left off.
//
// Without this, restarting the daemon (or the GUI, which spawns
// its own daemon) drops every torrent from the in-memory map and
// the user sees an empty download list — the bug that motivated
// this file.
//
// The manifest lives at <DataDir>/session.json and accompanying
// .torrent file copies (one per file-added torrent) live under
// <DataDir>/torrents/<infohash>.torrent. Magnet-added torrents
// don't need a .torrent on disk for restore — the magnet URI is
// stored in the manifest and re-added directly. When metadata
// arrives for a magnet-added torrent, the engine serialises the
// in-memory metainfo to the torrents/ dir and upgrades the
// manifest entry to AddedVia="file" so future restarts skip the
// metadata-fetch round trip.
type session struct {
	path        string
	torrentsDir string

	mu      sync.Mutex
	entries map[string]sessionEntry // keyed by 40-char hex infohash
}

// sessionEntry is one row in the on-disk manifest. JSON-stable.
type sessionEntry struct {
	InfoHash    string `json:"infohash"`
	AddedVia    string `json:"added_via"` // "magnet" | "file" | "infohash"
	MagnetURI   string `json:"magnet_uri,omitempty"`
	TorrentFile string `json:"torrent_file,omitempty"` // basename under <DataDir>/torrents/
	Paused      bool   `json:"paused,omitempty"`
	Indexing    bool   `json:"indexing"`
	QueueOrder  int64  `json:"queue_order,omitempty"`
	SignedBy    string `json:"signed_by,omitempty"`
}

type sessionFile struct {
	Version  int            `json:"version"`
	Torrents []sessionEntry `json:"torrents"`
}

const sessionFileVersion = 1

// loadSession opens the session manifest under dataDir. A missing
// file is not an error; the returned session starts empty and the
// first save creates the file. A corrupt manifest is logged at the
// caller side (this returns the parse error) so restart can fall
// back gracefully.
//
// dataDir == "" yields an in-memory-only session (every save is a
// no-op). Useful for tests and ephemeral engines.
func loadSession(dataDir string) (*session, error) {
	s := &session{entries: make(map[string]sessionEntry)}
	if dataDir == "" {
		return s, nil
	}
	s.path = filepath.Join(dataDir, "session.json")
	s.torrentsDir = filepath.Join(dataDir, "torrents")

	if err := os.MkdirAll(s.torrentsDir, 0o755); err != nil {
		return nil, fmt.Errorf("engine: mkdir torrents: %w", err)
	}

	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, fmt.Errorf("engine: read session: %w", err)
	}
	if len(raw) == 0 {
		return s, nil
	}
	var sf sessionFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		return nil, fmt.Errorf("engine: decode session: %w", err)
	}
	for _, e := range sf.Torrents {
		if len(e.InfoHash) != 40 {
			continue
		}
		s.entries[e.InfoHash] = e
	}
	return s, nil
}

// list returns every entry in queue-order (lowest first), then
// infohash as a tiebreaker. Returned slice is a copy.
func (s *session) list() []sessionEntry {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	out := make([]sessionEntry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, e)
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].QueueOrder != out[j].QueueOrder {
			return out[i].QueueOrder < out[j].QueueOrder
		}
		return out[i].InfoHash < out[j].InfoHash
	})
	return out
}

// update applies mut to the entry for infoHash (creating a new
// entry if none exists), then persists. Idempotent.
func (s *session) update(infoHash string, mut func(*sessionEntry)) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[infoHash]
	if !ok {
		e = sessionEntry{InfoHash: infoHash}
	}
	mut(&e)
	e.InfoHash = infoHash
	s.entries[infoHash] = e
	return s.saveLocked()
}

// remove drops the entry for infoHash and deletes the matching
// .torrent file copy (if any). Idempotent.
func (s *session) remove(infoHash string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	e, had := s.entries[infoHash]
	delete(s.entries, infoHash)
	err := s.saveLocked()
	s.mu.Unlock()
	if had && e.TorrentFile != "" && s.torrentsDir != "" {
		_ = os.Remove(filepath.Join(s.torrentsDir, e.TorrentFile))
	}
	return err
}

// saveLocked writes the in-memory entries to disk atomically.
// Caller must hold s.mu. No-op when path == "".
func (s *session) saveLocked() error {
	if s.path == "" {
		return nil
	}
	out := make([]sessionEntry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].QueueOrder != out[j].QueueOrder {
			return out[i].QueueOrder < out[j].QueueOrder
		}
		return out[i].InfoHash < out[j].InfoHash
	})
	body, err := json.MarshalIndent(sessionFile{
		Version:  sessionFileVersion,
		Torrents: out,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("engine: marshal session: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return fmt.Errorf("engine: write session tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("engine: rename session: %w", err)
	}
	return nil
}

// writeTorrentCopy persists raw .torrent bytes under the torrents/
// dir as <infoHash>.torrent. The returned basename is what callers
// store in sessionEntry.TorrentFile. Returns "" when the session
// has no torrents/ dir (in-memory-only mode).
func (s *session) writeTorrentCopy(infoHash string, raw []byte) (string, error) {
	if s == nil || s.torrentsDir == "" {
		return "", nil
	}
	name := infoHash + ".torrent"
	target := filepath.Join(s.torrentsDir, name)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return "", fmt.Errorf("engine: write torrent copy: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("engine: rename torrent copy: %w", err)
	}
	return name, nil
}
