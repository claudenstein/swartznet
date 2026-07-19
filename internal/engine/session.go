package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// sessionEntry is one persisted torrent. The JSON shape is frozen — existing
// session.json files must keep loading.
type sessionEntry struct {
	InfoHash    string `json:"infohash"`
	AddedVia    string `json:"added_via"` // magnet | file | infohash | metainfo
	MagnetURI   string `json:"magnet_uri,omitempty"`
	TorrentFile string `json:"torrent_file,omitempty"` // basename under <DataDir>/torrents/
	Paused      bool   `json:"paused,omitempty"`
	Indexing    bool   `json:"indexing"` // deliberately NOT omitempty
	QueueOrder  int64  `json:"queue_order,omitempty"`
	SignedBy    string `json:"signed_by,omitempty"`
	DataPath    string `json:"data_path,omitempty"`    // seed-from parent directory
	ContentName string `json:"content_name,omitempty"` // real on-disk basename
}

type sessionFile struct {
	Version  int            `json:"version"`
	Torrents []sessionEntry `json:"torrents"`
}

// session persists the torrent set. DataDir=="" yields an in-memory session
// where every save is a no-op.
type session struct {
	mu          sync.Mutex
	path        string // "" = in-memory
	torrentsDir string // "" = in-memory
	entries     map[string]sessionEntry
}

func newMemorySession() *session {
	return &session{entries: make(map[string]sessionEntry)}
}

// loadSession loads <dataDir>/session.json and prepares <dataDir>/torrents/.
// A missing or empty file is an empty session; corrupt JSON is an error the
// engine downgrades to a warning (never fails New).
func loadSession(dataDir string) (*session, error) {
	if dataDir == "" {
		return newMemorySession(), nil
	}
	s := &session{
		path:        filepath.Join(dataDir, "session.json"),
		torrentsDir: filepath.Join(dataDir, "torrents"),
		entries:     make(map[string]sessionEntry),
	}
	// A nil session return is FATAL to engine.New: without torrents/ no add
	// can persist, so the whole run would silently lose state.
	if err := os.MkdirAll(s.torrentsDir, 0o755); err != nil {
		return nil, fmt.Errorf("engine: mkdir torrents: %w", err)
	}
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) || (err == nil && len(raw) == 0) {
		return s, nil
	}
	if err != nil {
		// The session itself (with paths) is still returned so persistence
		// self-heals: the next save rewrites a valid manifest.
		return s, fmt.Errorf("engine: read session: %w", err)
	}
	var f sessionFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return s, fmt.Errorf("engine: decode session: %w", err)
	}
	for _, ent := range f.Torrents {
		if len(ent.InfoHash) != 40 {
			continue // silently drop malformed rows
		}
		s.entries[ent.InfoHash] = ent
	}
	return s, nil
}

// list returns entries sorted by queue_order asc, then infohash asc.
func (s *session) list() []sessionEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	return out
}

// update upserts the entry for infoHash and saves.
func (s *session) update(infoHash string, mut func(*sessionEntry)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ent := s.entries[infoHash]
	ent.InfoHash = infoHash
	mut(&ent)
	s.entries[infoHash] = ent
	return s.saveLocked()
}

// updateExisting mutates the entry only if it still exists — background
// writers (the magnet→metainfo upgrade) must never resurrect an entry a
// concurrent RemoveTorrent deleted. Reports whether the entry existed.
func (s *session) updateExisting(infoHash string, mut func(*sessionEntry)) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ent, ok := s.entries[infoHash]
	if !ok {
		return false, nil
	}
	mut(&ent)
	s.entries[infoHash] = ent
	return true, s.saveLocked()
}

// remove deletes the entry, saves, and best-effort removes the .torrent copy.
func (s *session) remove(infoHash string) {
	s.mu.Lock()
	ent, ok := s.entries[infoHash]
	delete(s.entries, infoHash)
	err := s.saveLocked()
	dir := s.torrentsDir
	s.mu.Unlock()
	_ = err
	if ok && ent.TorrentFile != "" && dir != "" {
		_ = os.Remove(filepath.Join(dir, ent.TorrentFile))
	}
}

// saveLocked rewrites the whole file atomically (.tmp+rename). No-op for
// in-memory sessions.
func (s *session) saveLocked() error {
	if s.path == "" {
		return nil
	}
	entries := make([]sessionEntry, 0, len(s.entries))
	for _, e := range s.entries {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].QueueOrder != entries[j].QueueOrder {
			return entries[i].QueueOrder < entries[j].QueueOrder
		}
		return entries[i].InfoHash < entries[j].InfoHash
	})
	body, err := json.MarshalIndent(sessionFile{Version: 1, Torrents: entries}, "", "  ")
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

// writeTorrentCopy stores a byte-exact .torrent copy as <ih>.torrent and
// returns its basename ("" for in-memory sessions).
func (s *session) writeTorrentCopy(infoHash string, raw []byte) (string, error) {
	s.mu.Lock()
	dir := s.torrentsDir
	s.mu.Unlock()
	if dir == "" {
		return "", nil
	}
	name := infoHash + ".torrent"
	dst := filepath.Join(dir, name)
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return "", fmt.Errorf("engine: write torrent copy: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("engine: rename torrent copy: %w", err)
	}
	return name, nil
}

// persistAdd records an add. MagnetURI/TorrentFile are written only when
// non-empty so a raced magnet→metainfo upgrade is never clobbered with "".
func (e *Engine) persistAdd(h *Handle, via, magnetURI, torrentFile string) {
	if err := e.sess.update(h.InfoHashHex(), func(ent *sessionEntry) {
		ent.AddedVia = via
		if magnetURI != "" {
			ent.MagnetURI = magnetURI
		}
		if torrentFile != "" {
			ent.TorrentFile = torrentFile
		}
		ent.Indexing = h.isIndexing()
		ent.Paused = h.isPaused()
		ent.QueueOrder = h.getQueueOrder()
		if sb := h.SignedBy(); sb != "" {
			ent.SignedBy = sb
		}
	}); err != nil {
		e.log.Warn("engine.session_update_err", "err", err)
	}
}

// persistState records a paused/indexing/queue-order mutation. It uses
// updateExisting, NOT update: a pause/resume/set-indexing request can race a
// concurrent RemoveTorrent (RemoveTorrent releases e.mu between deleting the
// handle and removing the session row), and an unconditional upsert here would
// resurrect the just-removed entry — which then re-adds the torrent on the next
// restart, silently rejoining a swarm the user explicitly left.
func (e *Engine) persistState(h *Handle) {
	if _, err := e.sess.updateExisting(h.InfoHashHex(), func(ent *sessionEntry) {
		ent.Paused = h.isPaused()
		ent.Indexing = h.isIndexing()
		ent.QueueOrder = h.getQueueOrder()
	}); err != nil {
		e.log.Warn("engine.session_update_err", "err", err)
	}
}
