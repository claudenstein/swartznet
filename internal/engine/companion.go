package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// This file owns the engine's half of the companion-index seams (Slice 10):
// the BEP-46 pointer put/get accessors, the metainfo seeder, and the
// FAIL-CLOSED companion fetch. The fail-closed bounds live HERE (not in the
// companion package) because the info dict is fully attacker-controlled — the
// BEP-46 pointer resolves to an untrusted infohash.

// maxCompanionBytes caps a companion torrent's single file. 32 MiB is far above
// any legitimate index yet rejects a gzip-bomb metainfo BEFORE the first piece
// is requested.
const maxCompanionBytes int64 = 32 << 20

// PointerPutter returns a BEP-46 pointer putter under the node's identity, or
// nil when the DHT is off or no signer is installed. The companion publisher
// consumes it (as companion.PointerPutter).
func (e *Engine) PointerPutter() *dhtindex.AnacrolixPutter {
	srv := e.dhtServer()
	e.recMu.Lock()
	priv, has := e.signer, e.hasSigner
	e.recMu.Unlock()
	if srv == nil || !has {
		return nil
	}
	p, err := dhtindex.NewAnacrolixPutter(srv, priv)
	if err != nil {
		return nil
	}
	return p
}

// PointerGetter returns a BEP-46 pointer getter, or nil when the DHT is off.
// The companion subscriber consumes it (as companion.PointerGetter).
func (e *Engine) PointerGetter() *dhtindex.AnacrolixGetter {
	srv := e.dhtServer()
	if srv == nil {
		return nil
	}
	g, err := dhtindex.NewAnacrolixGetter(srv)
	if err != nil {
		return nil
	}
	return g
}

// SeedMetaInfo seeds the companion .torrent as a COMPANION bookkeeping torrent
// (never indexed / minted / Layer-D published), serving its single file in
// place from contentPath (the companion dir, distinct from DataDir). It is not
// persisted to the session — the publisher re-creates it every refresh.
// Satisfies companion.TorrentSeeder.
func (e *Engine) SeedMetaInfo(mi *metainfo.MetaInfo, contentPath string) error {
	if mi == nil {
		return fmt.Errorf("engine: nil metainfo")
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return fmt.Errorf("engine: closed")
	}
	spec, err := torrent.TorrentSpecFromMetaInfoErr(mi)
	if err != nil {
		e.mu.Unlock()
		return fmt.Errorf("engine: companion seed spec: %w", err)
	}
	base, name := splitSeedRoot(contentPath)
	spec.Storage = seedStorage(base, name)
	t, _, err := e.client.AddTorrentSpec(spec)
	if err != nil {
		e.mu.Unlock()
		return fmt.Errorf("engine: companion seed: %w", err)
	}
	h, existed := e.registerLockedCompanion(t)
	e.mu.Unlock()
	if !existed {
		e.spawnVerify(h)
	}
	return nil
}

// DropTorrent drops a companion torrent (the publisher drops the previous
// snapshot's seed when it publishes a new one, so companion seeds do not
// accumulate). Satisfies companion.TorrentSeeder.
func (e *Engine) DropTorrent(ih [20]byte) error {
	return e.DropCompanionTorrent(ih)
}

// DropCompanionTorrent removes a companion handle: it closes the subscriptions,
// drops the anacrolix torrent (downloaded data stays on disk), and forgets the
// handle. No session removal or Layer-D retract — companion torrents are never
// persisted or published. A no-op when the infohash is absent.
func (e *Engine) DropCompanionTorrent(ih [20]byte) error {
	e.mu.Lock()
	h, ok := e.handles[metainfo.Hash(ih)]
	if !ok {
		e.mu.Unlock()
		return nil
	}
	delete(e.handles, metainfo.Hash(ih))
	e.mu.Unlock()
	h.markRemoved()
	h.pieceSub.Close()
	h.fileSub.Close()
	h.T.Drop()
	return nil
}

// addCompanionInfoHash adds an untrusted companion infohash as a COMPANION
// bookkeeping torrent (never indexed / minted / published) and does NOT persist
// it to the session. Used by FetchCompanionTorrent.
func (e *Engine) addCompanionInfoHash(ih metainfo.Hash) (*Handle, error) {
	if ih.IsZero() {
		return nil, fmt.Errorf("engine: zero infohash")
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, fmt.Errorf("engine: closed")
	}
	t, _ := e.client.AddTorrentInfoHash(ih)
	h, _ := e.registerLockedCompanion(t)
	e.mu.Unlock()
	return h, nil
}

// unsafeCompanionName rejects a filename that could escape DataDir or is
// otherwise unusable. Backslash is included so a Windows-authored manifest
// cannot smuggle a separator.
func unsafeCompanionName(name string) bool {
	return name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`)
}

// validateCompanionInfo enforces the fail-closed bounds on an untrusted
// companion info dict BEFORE any piece is requested: exactly one file, ≤32 MiB,
// and a safe filename. Factored out for direct unit-testing.
func validateCompanionInfo(info *metainfo.Info) error {
	if info == nil {
		return errors.New("engine: companion torrent has no info after GotInfo")
	}
	files := info.UpvertedFiles()
	if len(files) != 1 {
		return fmt.Errorf("engine: companion torrent has %d files, want exactly 1", len(files))
	}
	if info.TotalLength() > maxCompanionBytes {
		return fmt.Errorf("engine: companion torrent declares %d bytes, exceeds cap %d", info.TotalLength(), maxCompanionBytes)
	}
	if unsafeCompanionName(info.Name) {
		return fmt.Errorf("engine: companion torrent has unsafe name %q", info.Name)
	}
	return nil
}

// FetchCompanionTorrent adds an untrusted companion infohash, waits for
// metadata, enforces the fail-closed bounds, downloads the single file, and
// returns its on-disk path. Satisfies companion.CompanionFetcher.
func (e *Engine) FetchCompanionTorrent(ctx context.Context, infohash [20]byte) (string, error) {
	h, err := e.addCompanionInfoHash(metainfo.Hash(infohash))
	if err != nil {
		return "", err
	}
	// Drop the fetched torrent on EVERY exit path (success, timeout, bad bounds)
	// so a subscriber re-syncing hourly cannot accumulate handles/goroutines and
	// the shared on-disk path is not held by a stale torrent. T.Drop keeps the
	// downloaded file on disk for the caller to read.
	defer func() { _ = e.DropCompanionTorrent(infohash) }()

	select {
	case <-h.T.GotInfo():
	case <-ctx.Done():
		return "", ctx.Err()
	case <-e.bgCtx.Done():
		return "", errors.New("engine: closed")
	}
	info := h.T.Info()
	if err := validateCompanionInfo(info); err != nil {
		return "", err
	}
	// Bounds passed — download the single file.
	h.T.DownloadAll()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	target := h.T.Files()[0]
	for target.BytesCompleted() < target.Length() {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-e.bgCtx.Done():
			return "", errors.New("engine: closed")
		case <-ticker.C:
		}
	}
	return filepath.Join(e.cfg.DataDir, info.Name), nil
}
