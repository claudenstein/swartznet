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
	if !h.companion {
		// SAFETY: never drop a real (non-companion) torrent through the companion
		// path. A followed publisher controls the pointer infohash; without this
		// guard, naming a live torrent's infohash would tear that torrent down.
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

// retainAndAttachCompanion registers an in-flight fetch for infohash AND attaches
// (adds/looks up) the companion torrent handle ATOMICALLY under companionFetchMu.
// The atomicity is load-bearing: releaseCompanionFetch drops the shared torrent
// under the same lock, so a concurrent fetcher can never attach to a handle in
// the window between another fetcher's last-ref decrement and its drop — the
// attach either precedes the decrement (ref stays ≥1, no drop) or follows the
// drop (addCompanionInfoHash re-adds a fresh handle). Lock order is always
// companionFetchMu → e.mu (addCompanionInfoHash/DropCompanionTorrent take e.mu),
// and nothing takes them in the reverse order, so this cannot deadlock.
func (e *Engine) retainAndAttachCompanion(ih [20]byte) (*Handle, error) {
	e.companionFetchMu.Lock()
	defer e.companionFetchMu.Unlock()
	if e.companionFetchRefs == nil {
		e.companionFetchRefs = make(map[[20]byte]int)
	}
	e.companionFetchRefs[ih]++
	h, err := e.addCompanionInfoHash(metainfo.Hash(ih))
	if err != nil {
		// Attach failed → nothing to drop; just undo the ref we took.
		if e.companionFetchRefs[ih]--; e.companionFetchRefs[ih] <= 0 {
			delete(e.companionFetchRefs, ih)
		}
		return nil, err
	}
	return h, nil
}

// releaseCompanionFetch drops the shared companion torrent only when the LAST
// concurrent fetcher for infohash exits. The drop runs UNDER companionFetchMu so
// the "last ref → drop" decision and the teardown are atomic with a concurrent
// retainAndAttachCompanion (see its doc); otherwise a fetcher attaching in the
// gap would be left holding a torrent this call then tore down.
func (e *Engine) releaseCompanionFetch(ih [20]byte) {
	e.companionFetchMu.Lock()
	defer e.companionFetchMu.Unlock()
	n := e.companionFetchRefs[ih] - 1
	if n > 0 {
		e.companionFetchRefs[ih] = n
		return
	}
	delete(e.companionFetchRefs, ih)
	_ = e.DropCompanionTorrent(ih)
}

// FetchCompanionTorrent adds an untrusted companion infohash, waits for
// metadata, enforces the fail-closed bounds, downloads the single file, and
// returns its on-disk path. Satisfies companion.CompanionFetcher.
func (e *Engine) FetchCompanionTorrent(ctx context.Context, infohash [20]byte) (string, error) {
	// Retain + attach atomically (see retainAndAttachCompanion): registers this
	// fetch's interest and gets the shared handle in one critical section, so a
	// concurrent fetcher's last-ref drop can never tear the handle down between
	// our attach and our retain.
	h, err := e.retainAndAttachCompanion(infohash)
	if err != nil {
		return "", err
	}
	defer e.releaseCompanionFetch(infohash)
	// SAFETY (companion-collision defense): if the infohash collides with a real
	// (non-companion) torrent the node is already running, addCompanionInfoHash
	// returns that live handle. A followed publisher controls the pointer
	// infohash, so we must NOT let it (a) override that torrent's file priorities
	// [DownloadAll is gated on h.companion below] or (b) drop it
	// [DropCompanionTorrent refuses a non-companion handle]. We do not refuse the
	// fetch outright — a caller may legitimately fetch an infohash already present
	// as a plain handle — but a colliding real torrent is never mutated: the
	// companion-index decode simply fails and the fetch returns an error.
	//
	// The fetched torrent is dropped on EVERY exit path (success, timeout, bad
	// bounds) via the deferred releaseCompanionFetch above — reference-counted so
	// the drop waits for the LAST concurrent fetcher of this infohash (concurrent
	// aggregate lookups resolving one publisher share one handle). T.Drop keeps
	// the downloaded file on disk for the caller to read.
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
	// Bounds passed — download the single file, but ONLY for a handle we created
	// as a companion. If the infohash collided with a pre-existing (non-companion)
	// torrent, that torrent's own download path governs its file priorities; never
	// override them here (a followed publisher must not force a full download of a
	// real torrent by naming its infohash in a pointer).
	if h.companion {
		h.T.DownloadAll()
	}
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
