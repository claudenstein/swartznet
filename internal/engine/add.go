package engine

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"

	cbencode "github.com/swartznet/swartznet/contracts/bencode"
)

// AddMagnet adds a magnet URI. The URI is parsed locally FIRST: anacrolix
// panics on a zero infohash, and a daemon must never die on bad input.
func (e *Engine) AddMagnet(uri string) (*Handle, error) {
	m, err := metainfo.ParseMagnetUri(uri)
	if err != nil {
		return nil, fmt.Errorf("engine: parse magnet: %w", err)
	}
	if m.InfoHash.IsZero() {
		return nil, fmt.Errorf("engine: magnet has zero infohash (caller must provide a non-empty xt=urn:btih:... value)")
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, fmt.Errorf("engine: closed")
	}
	t, err := e.client.AddMagnet(uri)
	if err != nil {
		e.mu.Unlock()
		return nil, fmt.Errorf("engine: add magnet: %w", err)
	}
	h, existed := e.registerLocked(t, false)
	e.mu.Unlock()
	if !existed {
		e.persistAdd(h, "magnet", uri, "")
		go e.upgradeMagnetSession(h)
	}
	return h, nil
}

// AddMagnetURI is the HTTP-facing magnet add: recover-wrapped so pathological
// input becomes a 400, never a daemon crash. Returns the 40-hex infohash.
func (e *Engine) AddMagnetURI(uri string) (infohash string, err error) {
	defer func() {
		if r := recover(); r != nil {
			infohash, err = "", fmt.Errorf("engine: AddMagnetURI panic: %v", r)
		}
	}()
	h, err := e.AddMagnet(uri)
	if err != nil {
		return "", err
	}
	return h.InfoHashHex(), nil
}

// AddTorrentFile adds a .torrent from disk.
func (e *Engine) AddTorrentFile(path string) (*Handle, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("engine: read .torrent: %w", err)
	}
	return e.AddTorrentBytes(raw)
}

// AddTorrentBytes adds a .torrent from raw bytes (file or stdin). The
// ORIGINAL bytes — never a re-marshal — are what get copied into the session
// torrents/ dir, preserving top-level snet.* fields and exotic bencode
// byte-for-byte. (Slice 3 inserts signature verification of these same raw
// bytes right here.)
func (e *Engine) AddTorrentBytes(raw []byte) (*Handle, error) {
	// The contracts codec validates strictly (trailing bytes, missing info)
	// and derives the infohash from the raw info bytes — the frozen
	// derivation signing builds on.
	view, err := cbencode.ParseMetainfo(raw)
	if err != nil {
		return nil, fmt.Errorf("engine: load .torrent: %w", err)
	}
	mi, err := metainfo.Load(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("engine: load .torrent: %w", err)
	}
	if got := mi.HashInfoBytes().HexString(); got != view.InfoHashHex() {
		// Cannot happen unless one codec is broken — a contracts-tier bug.
		return nil, fmt.Errorf("engine: load .torrent: infohash codec drift (%s vs %s)", view.InfoHashHex(), got)
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, fmt.Errorf("engine: closed")
	}
	t, err := e.client.AddTorrent(mi)
	if err != nil {
		e.mu.Unlock()
		return nil, fmt.Errorf("engine: add torrent: %w", err)
	}
	h, existed := e.registerLocked(t, false)
	e.mu.Unlock()
	if !existed {
		tname, cpErr := e.sess.writeTorrentCopy(h.InfoHashHex(), raw)
		if cpErr != nil {
			e.log.Warn("engine.session_torrent_copy_err", "info_hash", h.InfoHashHex(), "err", cpErr)
		}
		e.persistAdd(h, "file", "", tname)
	}
	return h, nil
}

// AddInfoHash adds a bare infohash. The value is attacker-controlled (bare
// CLI hex today, BEP-46 pointers later), so everything past the zero check
// is recover-wrapped.
func (e *Engine) AddInfoHash(hash metainfo.Hash) (h *Handle, err error) {
	if hash.IsZero() {
		return nil, fmt.Errorf("engine: zero infohash")
	}
	defer func() {
		if r := recover(); r != nil {
			h, err = nil, fmt.Errorf("engine: AddInfoHash panic: %v", r)
		}
	}()
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, fmt.Errorf("engine: closed")
	}
	t, _ := e.client.AddTorrentInfoHash(hash)
	h, existed := e.registerLocked(t, false)
	e.mu.Unlock()
	if !existed {
		e.persistAdd(h, "infohash", "", "")
		go e.upgradeMagnetSession(h)
	}
	return h, nil
}

// AddTorrentMetaInfo adds an in-memory metainfo stored under DataDir.
func (e *Engine) AddTorrentMetaInfo(mi *metainfo.MetaInfo) (*Handle, error) {
	return e.addTorrentMetaInfo(mi, "")
}

// AddTorrentMetaInfoSeedFrom adds a metainfo whose content already lives at
// contentRoot (the exact path that was hashed) — seeding in place.
func (e *Engine) AddTorrentMetaInfoSeedFrom(mi *metainfo.MetaInfo, contentRoot string) (*Handle, error) {
	return e.addTorrentMetaInfo(mi, contentRoot)
}

func (e *Engine) addTorrentMetaInfo(mi *metainfo.MetaInfo, contentRoot string) (*Handle, error) {
	if mi == nil {
		return nil, fmt.Errorf("engine: nil metainfo")
	}
	var (
		t   *torrent.Torrent
		err error
	)
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, fmt.Errorf("engine: closed")
	}
	base, name := "", ""
	if contentRoot == "" {
		t, err = e.client.AddTorrent(mi)
		if err != nil {
			err = fmt.Errorf("engine: add torrent metainfo: %w", err)
		}
	} else {
		var spec *torrent.TorrentSpec
		spec, err = torrent.TorrentSpecFromMetaInfoErr(mi)
		if err != nil {
			err = fmt.Errorf("engine: build torrent spec: %w", err)
		} else {
			base, name = splitSeedRoot(contentRoot)
			spec.Storage = seedStorage(base, name)
			t, _, err = e.client.AddTorrentSpec(spec)
			if err != nil {
				err = fmt.Errorf("engine: add torrent metainfo: %w", err)
			}
		}
	}
	if err != nil {
		e.mu.Unlock()
		return nil, err
	}
	h, existed := e.registerLocked(t, false)
	e.mu.Unlock()

	if !existed {
		// Background rehash: anacrolix verifies lazily on peer request, so a
		// brand-new seed would sit at 0% forever without this. Duplicate adds
		// skip it — re-hashing a multi-GB seed per repeat add is pure waste.
		go func() {
			if err := h.T.VerifyDataContext(e.bgCtx); err != nil && e.bgCtx.Err() == nil {
				e.log.Debug("engine.verify_data_err", "info_hash", h.InfoHashHex(), "err", err)
			}
		}()
	}

	if !existed {
		raw, mErr := bencode.Marshal(*mi)
		tname := ""
		if mErr != nil {
			e.log.Warn("engine.session_metainfo_marshal_err", "info_hash", h.InfoHashHex(), "err", mErr)
		} else {
			var cpErr error
			tname, cpErr = e.sess.writeTorrentCopy(h.InfoHashHex(), raw)
			if cpErr != nil {
				e.log.Warn("engine.session_torrent_copy_err", "info_hash", h.InfoHashHex(), "err", cpErr)
			}
		}
		e.persistAdd(h, "metainfo", "", tname)
		if contentRoot != "" {
			if err := e.sess.update(h.InfoHashHex(), func(ent *sessionEntry) {
				ent.DataPath = base
				ent.ContentName = name
			}); err != nil {
				e.log.Warn("engine.session_update_err", "info_hash", h.InfoHashHex(), "err", err)
			}
		}
	}
	return h, nil
}

// splitSeedRoot splits a content root into (parent, basename), trimming a
// trailing separator first so "foo/bar/" yields ("foo","bar"), not
// ("foo/bar",".").
func splitSeedRoot(root string) (dir, base string) {
	trimmed := strings.TrimRight(root, string(filepath.Separator))
	return filepath.Dir(trimmed), filepath.Base(trimmed)
}

// seedStorage builds file storage keyed on the REAL on-disk basename — not
// info.Name — so a renamed torrent still seeds in place (the legacy
// "downloading 0%" fix). Piece completion is in-memory on purpose: never
// drop a completion DB into the user's source directory; VerifyData
// repopulates it.
func seedStorage(baseDir, contentName string) storage.ClientImpl {
	return storage.NewFileOpts(storage.NewFileClientOpts{
		ClientBaseDir: baseDir,
		FilePathMaker: func(o storage.FilePathMakerOpts) string {
			return filepath.Join(append([]string{contentName}, o.File.BestPath()...)...)
		},
		PieceCompletion: storage.NewMapPieceCompletion(),
	})
}

// upgradeMagnetSession persists the fetched metainfo of a magnet/infohash
// add so restore skips the metadata fetch. It must NEVER run for file or
// metainfo entries: a re-marshalled metainfo can differ byte-wise from the
// original, and metainfo.Load would then reject the copy with "expected EOF"
// on the next restore, bricking that entry.
func (e *Engine) upgradeMagnetSession(h *Handle) {
	if e.sess == nil {
		return
	}
	select {
	case <-h.T.GotInfo():
	case <-e.bgCtx.Done():
		return
	case <-h.removed:
		return
	case <-time.After(10 * time.Minute):
		return
	}
	// Re-check removal after the (possibly minutes-long) metadata wait: a
	// blind upsert here would resurrect an entry RemoveTorrent just deleted.
	select {
	case <-h.removed:
		return
	default:
	}
	ihHex := h.InfoHashHex()
	skip := false
	for _, ent := range e.sess.list() {
		if ent.InfoHash == ihHex {
			if ent.TorrentFile != "" || ent.AddedVia == "file" || ent.AddedVia == "metainfo" {
				skip = true
			}
			break
		}
	}
	if skip {
		return
	}
	mi := h.T.Metainfo()
	raw, err := bencode.Marshal(mi)
	if err != nil {
		e.log.Warn("engine.session_metainfo_marshal_err", "info_hash", ihHex, "err", err)
		return
	}
	tname, err := e.sess.writeTorrentCopy(ihHex, raw)
	if err != nil {
		e.log.Warn("engine.session_torrent_copy_err", "info_hash", ihHex, "err", err)
		return
	}
	if tname == "" {
		return
	}
	// updateExisting: no-op when RemoveTorrent won the race — never
	// resurrect a deleted entry, and don't leave its orphan copy behind.
	existed, err := e.sess.updateExisting(ihHex, func(ent *sessionEntry) {
		ent.TorrentFile = tname // AddedVia stays magnet/infohash for posterity
	})
	if err != nil {
		e.log.Warn("engine.session_update_err", "info_hash", ihHex, "err", err)
	}
	if !existed {
		_ = os.Remove(filepath.Join(e.sess.torrentsDir, tname))
	}
}
