package engine

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

// RestoreSession re-adds every persisted torrent. Called by the daemon right
// after engine construction, before the HTTP API starts. One bad entry never
// blocks the rest: per-entry failures warn and continue.
func (e *Engine) RestoreSession() error {
	if e.sess == nil {
		return nil
	}
	entries := e.sess.list()
	if len(entries) == 0 {
		return nil
	}
	for _, ent := range entries {
		if err := e.restoreEntry(ent); err != nil {
			e.log.Warn("engine.session_restore_failed", "info_hash", ent.InfoHash, "added_via", ent.AddedVia, "err", err)
			continue
		}
		e.log.Info("engine.session_restored", "info_hash", ent.InfoHash, "via", ent.AddedVia, "paused", ent.Paused, "indexing", ent.Indexing)
	}
	return nil
}

func (e *Engine) restoreEntry(ent sessionEntry) error {
	var t *torrent.Torrent

	switch {
	case ent.TorrentFile != "" && e.sess.torrentsDir != "":
		// A hand-edited manifest must not read outside torrents/.
		if ent.TorrentFile != filepath.Base(ent.TorrentFile) || ent.TorrentFile == "." || ent.TorrentFile == ".." {
			return fmt.Errorf("engine: session entry has unsafe torrent file name %q", ent.TorrentFile)
		}
		raw, err := os.ReadFile(filepath.Join(e.sess.torrentsDir, ent.TorrentFile))
		if err != nil {
			return err
		}
		mi, err := metainfo.Load(bytes.NewReader(raw))
		if err != nil {
			return err
		}
		// The copy's content must match the entry key, or removal of the
		// real torrent would strand a zombie entry that re-adds forever.
		if got := mi.HashInfoBytes().HexString(); got != ent.InfoHash {
			return fmt.Errorf("engine: session torrent copy infohash %s does not match entry %s", got, ent.InfoHash)
		}
		if ent.DataPath != "" {
			spec, err := torrent.TorrentSpecFromMetaInfoErr(mi)
			if err != nil {
				return fmt.Errorf("engine: build torrent spec: %w", err)
			}
			name := ent.ContentName
			if name == "" {
				// Pre-content_name legacy entries fall back to info.Name.
				info, err := mi.UnmarshalInfo()
				if err != nil {
					return err
				}
				name = info.Name
			}
			spec.Storage = seedStorage(ent.DataPath, name)
			t, _, err = e.client.AddTorrentSpec(spec)
			if err != nil {
				return err
			}
		} else {
			var err error
			t, err = e.client.AddTorrent(mi)
			if err != nil {
				return err
			}
		}
	case ent.MagnetURI != "":
		var err error
		t, err = e.client.AddMagnet(ent.MagnetURI)
		if err != nil {
			return err
		}
	default:
		var hash metainfo.Hash
		if err := hash.FromHexString(ent.InfoHash); err != nil {
			return err
		}
		t, _ = e.client.AddTorrentInfoHash(hash)
	}
	if t == nil {
		return fmt.Errorf("engine: nil torrent after add")
	}

	// Persisted indexing/signedBy/queueOrder are applied inside
	// registerLockedRestore BEFORE the index/download goroutines spawn, so a
	// cached-metainfo restore (GotInfo already closed) can't race autoIndex
	// into indexing an OFF torrent or writing a blank signed_by.
	e.mu.Lock()
	h, existed := e.registerLockedRestore(t, ent.Paused, &ent, false)
	e.mu.Unlock()
	if existed {
		return nil
	}

	if ent.Paused {
		t.DisallowDataDownload()
		t.DisallowDataUpload()
	}
	if ent.AddedVia != "file" && ent.AddedVia != "metainfo" {
		go e.upgradeMagnetSession(h)
	}
	go e.verifyOnRestore(h)
	return nil
}

// verifyOnRestore rehashes existing data so a restarted partial download
// resumes at its real percentage — anacrolix reads storage lazily and
// BytesCompleted stays 0 without it.
func (e *Engine) verifyOnRestore(h *Handle) {
	select {
	case <-h.T.GotInfo():
	case <-e.bgCtx.Done():
		return
	case <-h.removed:
		return
	case <-time.After(10 * time.Minute):
		return
	}
	if err := h.T.VerifyDataContext(e.bgCtx); err != nil && e.bgCtx.Err() == nil {
		e.log.Debug("engine.verify_data_err", "info_hash", h.InfoHashHex(), "err", err)
	}
}
