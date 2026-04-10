package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/indexer"
)

// Engine owns an anacrolix/torrent Client and wires SwartzNet's extension
// hooks into it. Construct with New; always Close when done.
//
// An Engine optionally holds a reference to an *indexer.Index (set via
// SetIndex). When present, new torrents are indexed automatically once their
// metadata arrives — no explicit IndexTorrent call from the CLI is needed.
type Engine struct {
	cfg    config.Config
	client *torrent.Client
	log    *slog.Logger
	idx    *indexer.Index // nil-safe; may be unset for headless tests

	mu       sync.Mutex
	closed   bool
	handles  map[metainfo.Hash]*Handle
	closeErr error
}

// SetIndex attaches an *indexer.Index to the engine. Any torrents that
// arrive after this call will be auto-indexed once their metadata is
// available. Safe to call at most once per Engine; calling it twice replaces
// the attached index without tearing down the old one.
func (e *Engine) SetIndex(idx *indexer.Index) {
	e.mu.Lock()
	e.idx = idx
	e.mu.Unlock()
}

// Handle is SwartzNet's wrapper around a *torrent.Torrent. It owns a piece
// state subscription (for live progress UI) AND a file-completion tracker
// (M2.1) whose events feed the M2.2 text extractor pipeline.
//
// Both subscriptions are torn down by Engine.Close via Handle's internal
// close hooks; callers do not need to do anything explicit.
type Handle struct {
	// T is the underlying anacrolix torrent. Exported for read-only callers
	// that need fields we haven't re-exported yet; prefer the wrapper methods
	// where available.
	T *torrent.Torrent

	// pieceSub is the live subscription to T.SubscribePieceStateChanges. It
	// fans events out on PieceEvents (via the Events accessor).
	pieceSub *pieceSubscription

	// fileSub is the file-completion tracker that watches piece events and
	// emits one FileCompleteEvent per file the first time that file reaches
	// a fully-verified state.
	fileSub *fileTracker
}

// PieceEvents returns a receive-only channel of piece state-change events
// for this torrent. Readers MUST drain this channel; if they fall behind,
// events are dropped (see piece_sub.go for the drop policy). Useful for
// live-progress UI code.
func (h *Handle) PieceEvents() <-chan torrent.PieceStateChange {
	return h.pieceSub.Events()
}

// FileEvents returns a receive-only channel of FileCompleteEvent values,
// each fired once when a file inside this torrent becomes fully complete.
// The channel is closed when the Engine is closed.
func (h *Handle) FileEvents() <-chan FileCompleteEvent {
	return h.fileSub.Events()
}

// New constructs an Engine with the given config. The config is validated and
// the data directory is created if missing. The underlying Client is started
// in the background (it listens for peers and joins the DHT if enabled).
func New(ctx context.Context, cfg config.Config, log *slog.Logger) (*Engine, error) {
	_ = ctx // reserved: future versions may use ctx for bootstrap timeouts
	if log == nil {
		log = slog.Default()
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	tc := torrent.NewDefaultClientConfig()
	tc.DataDir = cfg.DataDir
	tc.ListenPort = cfg.ListenPort
	tc.Seed = cfg.Seed
	tc.NoUpload = cfg.NoUpload
	tc.NoDHT = cfg.DisableDHT
	if cfg.HTTPUserAgent != "" {
		tc.HTTPUserAgent = cfg.HTTPUserAgent
	}

	// Wire the extension-point callbacks. These are the exact hook points
	// the integration design depends on. Right now they just log; M2 and
	// M3 will attach the indexer and the sn_search handler here.
	tc.Callbacks.StatusUpdated = append(tc.Callbacks.StatusUpdated,
		func(ev torrent.StatusUpdatedEvent) {
			log.Debug("torrent.status",
				"event", ev.Event,
				"info_hash", ev.InfoHash,
				"url", ev.Url,
				"err", ev.Error,
			)
		},
	)
	tc.Callbacks.PeerConnAdded = append(tc.Callbacks.PeerConnAdded,
		func(pc *torrent.PeerConn) {
			// M3 TODO: register "sn_search" in pc.LocalLtepProtocolMap via
			// AddUserProtocol. The anacrolix/torrent callback contract says
			// this is the correct place to do it: the extended handshake
			// has not yet occurred, so we can still influence which
			// extension names we advertise to this peer.
			log.Debug("peer.added", "peer", pc.RemoteAddr.String())
		},
	)

	cl, err := torrent.NewClient(tc)
	if err != nil {
		return nil, fmt.Errorf("engine: new client: %w", err)
	}

	log.Info("engine.started",
		"data_dir", cfg.DataDir,
		"listen_port", cl.LocalPort(),
		"peer_id", fmt.Sprintf("%x", cl.PeerID()),
		"dht_enabled", !cfg.DisableDHT,
	)

	return &Engine{
		cfg:     cfg,
		client:  cl,
		log:     log,
		handles: make(map[metainfo.Hash]*Handle),
	}, nil
}

// AddMagnet queues a magnet URI for download. The returned Handle exposes a
// piece-state subscription; callers MUST drain PieceEvents (via Next) or call
// Handle.Close to avoid blocking anacrolix's internal publisher.
func (e *Engine) AddMagnet(uri string) (*Handle, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, errors.New("engine: closed")
	}
	t, err := e.client.AddMagnet(uri)
	if err != nil {
		return nil, fmt.Errorf("engine: add magnet: %w", err)
	}
	return e.registerLocked(t), nil
}

// AddTorrentFile loads a .torrent file from disk and adds it to the swarm.
func (e *Engine) AddTorrentFile(path string) (*Handle, error) {
	mi, err := metainfo.LoadFromFile(path)
	if err != nil {
		return nil, fmt.Errorf("engine: load .torrent: %w", err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, errors.New("engine: closed")
	}
	t, err := e.client.AddTorrent(mi)
	if err != nil {
		return nil, fmt.Errorf("engine: add torrent: %w", err)
	}
	return e.registerLocked(t), nil
}

// registerLocked adds a torrent handle to the tracking map and starts its
// piece-state subscription. The caller must hold e.mu. It also kicks off a
// background goroutine that waits for torrent metadata and then indexes the
// torrent-level document into the attached *indexer.Index, if any.
func (e *Engine) registerLocked(t *torrent.Torrent) *Handle {
	ih := t.InfoHash()
	if h, ok := e.handles[ih]; ok {
		// Duplicate add; return the existing handle. anacrolix/torrent itself
		// already dedupes under the hood, so this is just the caller-facing
		// mirror of that.
		return h
	}
	h := &Handle{
		T:        t,
		pieceSub: startPieceSubscription(t, e.log),
		fileSub:  startFileTracker(t, e.log),
	}
	e.handles[ih] = h
	go e.autoIndex(h)
	return h
}

// autoIndex waits for a torrent's metadata to arrive and then indexes the
// torrent-level document into the attached *indexer.Index. If no index is
// attached, this is a no-op. Runs in a background goroutine; the waiting
// path is cancelled if Close is called on the engine.
func (e *Engine) autoIndex(h *Handle) {
	select {
	case <-h.T.GotInfo():
	case <-time.After(5 * time.Minute):
		e.log.Warn("indexer.autoindex.timeout", "info_hash", h.T.InfoHash().HexString())
		return
	}

	e.mu.Lock()
	idx := e.idx
	closed := e.closed
	e.mu.Unlock()
	if idx == nil || closed {
		return
	}

	doc := indexerDocFromTorrent(h.T)
	if err := idx.IndexTorrent(doc); err != nil {
		e.log.Warn("indexer.index_failed", "info_hash", doc.InfoHash, "err", err)
		return
	}
	e.log.Info("indexer.indexed",
		"info_hash", doc.InfoHash,
		"name", doc.Name,
		"files", doc.FileCount,
		"size", doc.SizeBytes,
	)
}

// indexerDocFromTorrent extracts a TorrentDoc from a live anacrolix/torrent
// Torrent. Caller must have already waited for GotInfo.
func indexerDocFromTorrent(t *torrent.Torrent) indexer.TorrentDoc {
	files := t.Files()
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.DisplayPath())
	}
	mi := t.Metainfo()
	trackers := mi.UpvertedAnnounceList().DistinctValues()
	if len(trackers) == 0 && mi.Announce != "" {
		trackers = []string{mi.Announce}
	}
	return indexer.TorrentDoc{
		InfoHash:  t.InfoHash().HexString(),
		Name:      t.Name(),
		FilePaths: paths,
		Trackers:  trackers,
		SizeBytes: t.Length(),
		FileCount: len(paths),
	}
}

// Torrents returns a snapshot of all known torrent handles. The slice is
// newly allocated each call and is safe to iterate without holding the
// Engine lock.
func (e *Engine) Torrents() []*Handle {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]*Handle, 0, len(e.handles))
	for _, h := range e.handles {
		out = append(out, h)
	}
	return out
}

// Close tears down the Client and all handles. Safe to call multiple times;
// only the first call performs real work. The returned error is the one from
// the underlying Client shutdown, if any.
func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return e.closeErr
	}
	e.closed = true
	for _, h := range e.handles {
		h.pieceSub.Close()
		h.fileSub.Close()
	}
	errs := e.client.Close()
	if len(errs) > 0 {
		e.closeErr = errors.Join(errs...)
	}
	e.log.Info("engine.closed", "err", e.closeErr)
	return e.closeErr
}

// LocalPort returns the TCP/uTP port the engine is actually listening on.
// Useful when the caller configured ListenPort = 0 and needs the resolved
// value (tests, LAN discovery, etc.).
func (e *Engine) LocalPort() int {
	return e.client.LocalPort()
}
