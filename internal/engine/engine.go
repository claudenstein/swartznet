package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	pp "github.com/anacrolix/torrent/peer_protocol"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/dhtindex"
	"github.com/swartznet/swartznet/internal/identity"
	"github.com/swartznet/swartznet/internal/indexer"
	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// Engine owns an anacrolix/torrent Client and wires SwartzNet's extension
// hooks into it. Construct with New; always Close when done.
//
// An Engine optionally holds a reference to an *indexer.Index (set via
// SetIndex). When present, new torrents are indexed automatically once their
// metadata arrives, and a content-ingestion Pipeline runs per-Handle to
// feed completed files through the text-extractor → content-index path.
//
// Every Engine also owns a *swarmsearch.Protocol which advertises the
// sn_search BEP-10 extension to every peer we connect to and tracks which
// remote peers speak it back. External packages reach the protocol via
// Engine.SwarmSearch().
type Engine struct {
	cfg      config.Config
	client   *torrent.Client
	log      *slog.Logger
	idx      *indexer.Index        // nil-safe; may be unset for headless tests
	pipeline *indexer.Pipeline     // nil iff idx == nil
	swarm    *swarmsearch.Protocol // always non-nil after New
	peers    *peerTracker          // addr → *torrent.PeerConn, for swarmSender

	identity  *identity.Identity   // ed25519 publisher keypair, nil for tests
	publisher *dhtindex.Publisher  // nil if no DHT or no identity
	manifest  *dhtindex.Manifest   // owned by publisher; nil iff publisher nil
	lookup    *dhtindex.Lookup     // M4e DHT keyword lookup; nil iff no DHT

	mu       sync.Mutex
	closed   bool
	handles  map[metainfo.Hash]*Handle
	closeErr error
}

// Publisher returns the engine's DHT keyword publisher, or nil if
// the engine was constructed without one (no DHT, no identity, or a
// headless test setup).
func (e *Engine) Publisher() *dhtindex.Publisher { return e.publisher }

// Identity returns the engine's persistent ed25519 keypair, or nil
// if no identity was loaded.
func (e *Engine) Identity() *identity.Identity { return e.identity }

// Lookup returns the engine's DHT keyword lookup handle, or nil if
// no DHT server is available. If we have an identity, our own pubkey
// is automatically added as a known indexer so we can find our own
// published entries during testing.
func (e *Engine) Lookup() *dhtindex.Lookup { return e.lookup }

// peerTracker maintains a thread-safe address → *torrent.PeerConn map.
// Populated by the PeerConnAdded callback and cleaned by PeerConnClosed.
// swarmSender reads it to look up a specific peer when Query() fans a
// message out by address.
type peerTracker struct {
	mu    sync.RWMutex
	conns map[string]*torrent.PeerConn
}

func newPeerTracker() *peerTracker {
	return &peerTracker{conns: make(map[string]*torrent.PeerConn)}
}

func (pt *peerTracker) add(addr string, pc *torrent.PeerConn) {
	pt.mu.Lock()
	pt.conns[addr] = pc
	pt.mu.Unlock()
}

func (pt *peerTracker) remove(addr string) {
	pt.mu.Lock()
	delete(pt.conns, addr)
	pt.mu.Unlock()
}

func (pt *peerTracker) get(addr string) (*torrent.PeerConn, bool) {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	pc, ok := pt.conns[addr]
	return pc, ok
}

// SwarmSearch returns the engine's sn_search protocol handle. Callers
// (the CLI, future REST layer) use this to issue outbound swarm
// queries, inspect known peers, and override capabilities.
func (e *Engine) SwarmSearch() *swarmsearch.Protocol {
	return e.swarm
}

// SetIndex attaches an *indexer.Index to the engine and starts a
// content-ingestion Pipeline backed by that index. Any torrents that
// arrive after this call will be auto-indexed once their metadata is
// available, and their files will be extracted + content-indexed as they
// complete on disk.
//
// As a side-effect, this also wires the sn_search Protocol's
// LocalSearcher to the same index, so inbound sn_search queries from
// peers get answered against the same content the CLI searches
// locally. Pass a nil index to unwire both the pipeline AND the
// sn_search LocalSearcher.
//
// Safe to call at most once per Engine. Calling it twice replaces the
// attached index and pipeline; the old pipeline is stopped cleanly first.
func (e *Engine) SetIndex(idx *indexer.Index) {
	e.mu.Lock()
	// Stop any pre-existing pipeline before swapping in a new one.
	if e.pipeline != nil {
		e.pipeline.Stop()
	}
	e.idx = idx
	if idx != nil {
		e.pipeline = indexer.NewPipeline(idx, e.log, 0)
		e.pipeline.Start()
		e.swarm.SetSearcher(&indexerSearcher{idx: idx})
	} else {
		e.pipeline = nil
		e.swarm.SetSearcher(nil)
	}
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

	// Construct the swarm-search protocol before wiring callbacks — it
	// owns the per-peer state the callbacks will populate.
	swarm := swarmsearch.New(log)

	// Wire the extension-point callbacks. These are the exact hook points
	// the integration design depends on.
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
	// peers is the map the swarmSender (see swarmadapter.go) consults
	// when fanning a query out to a specific address. Declared here
	// so the callbacks can close over it; the Engine holds the same
	// pointer below.
	peers := newPeerTracker()

	tc.Callbacks.PeerConnAdded = append(tc.Callbacks.PeerConnAdded,
		func(pc *torrent.PeerConn) {
			// Per anacrolix's callback contract: "This is a good time to
			// alter the supported extension protocols." We add sn_search
			// to pc.LocalLtepProtocolMap so our outbound LTEP handshake
			// advertises it to this peer, then record the peer in our
			// own state maps.
			swarm.AdvertiseOn(pc.LocalLtepProtocolMap)
			addr := pc.RemoteAddr.String()
			swarm.NotePeerAdded(addr)
			peers.add(addr, pc)
		},
	)
	// ReadExtendedHandshake fires after the remote peer sends its LTEP
	// handshake. The protocol uses it to see whether the peer also
	// advertises sn_search and, if so, to record the extension id they
	// chose for it (which we need to send them messages later).
	tc.Callbacks.ReadExtendedHandshake = func(pc *torrent.PeerConn, hs *pp.ExtendedHandshakeMessage) {
		swarm.OnRemoteHandshake(pc.RemoteAddr.String(), hs)
	}
	// PeerConnReadExtensionMessage fires when an LTEP extended message
	// arrives. We filter to sn_search frames by looking up the local
	// extension id in the peer's map, and dispatch to the protocol
	// with a reply closure bound to this exact connection.
	tc.Callbacks.PeerConnReadExtensionMessage = append(
		tc.Callbacks.PeerConnReadExtensionMessage,
		func(ev torrent.PeerConnReadExtensionMessageEvent) {
			name, _, err := ev.PeerConn.LocalLtepProtocolMap.LookupId(ev.ExtensionNumber)
			if err != nil || name != swarmsearch.ExtensionName {
				return
			}
			peerAddr := ev.PeerConn.RemoteAddr.String()
			reply := func(body []byte) error {
				return ev.PeerConn.WriteExtendedMessage(swarmsearch.ExtensionName, body)
			}
			swarm.HandleMessage(peerAddr, ev.Payload, reply)
		},
	)
	// PeerConnClosed lets us drop stale peer state from the
	// sn_search tracker so long-running processes do not leak memory.
	tc.Callbacks.PeerConnClosed = func(pc *torrent.PeerConn) {
		addr := pc.RemoteAddr.String()
		swarm.OnPeerClosed(addr)
		peers.remove(addr)
	}

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

	eng := &Engine{
		cfg:     cfg,
		client:  cl,
		log:     log,
		swarm:   swarm,
		handles: make(map[metainfo.Hash]*Handle),
		peers:   peers,
	}
	// Hand the peer tracker to the swarmSender so Query fan-out can
	// find specific peers by address. The callbacks above and this
	// sender share the same peerTracker instance.
	swarm.SetSender(&swarmSender{peers: peers})

	// Load (or create) the persistent identity, then start a DHT
	// publisher backed by it. Failures here are non-fatal: a node
	// without an identity / publisher still works for download +
	// local search + Layer-S queries; it just doesn't push entries
	// into the mainline DHT.
	if cfg.IdentityPath != "" {
		id, err := identity.LoadOrCreate(cfg.IdentityPath)
		if err != nil {
			log.Warn("engine.identity_load_err", "err", err)
		} else {
			eng.identity = id
			log.Info("engine.identity_loaded", "pubkey", id.PublicKeyHex())
			if err := eng.startPublisher(); err != nil {
				log.Warn("engine.publisher_start_err", "err", err)
			}
		}
	}
	return eng, nil
}

// startPublisher constructs the DHT keyword publisher AND lookup if
// conditions are met (an identity is loaded, the underlying torrent
// client exposes an anacrolix DHT server). Called once from
// engine.New.
func (e *Engine) startPublisher() error {
	if e.identity == nil {
		return errors.New("engine: no identity")
	}
	srv := e.dhtServer()
	if srv == nil {
		return errors.New("engine: no anacrolix DHT server available")
	}
	put, err := dhtindex.NewAnacrolixPutter(srv, e.identity.PrivateKey)
	if err != nil {
		return fmt.Errorf("engine: new anacrolix putter: %w", err)
	}
	mf, err := dhtindex.LoadOrCreateManifest(e.cfg.PublisherManifest)
	if err != nil {
		return fmt.Errorf("engine: load publisher manifest: %w", err)
	}
	e.manifest = mf
	e.publisher = dhtindex.NewPublisher(put, mf, dhtindex.DefaultPublisherOptions(), e.log)
	e.publisher.Start()
	e.log.Info("engine.publisher_started", "manifest", e.cfg.PublisherManifest)

	// Build the matching lookup handle. Self-pubkey is added as a
	// known indexer so the user can `swartznet search --dht` against
	// their own freshly-published entries during local testing.
	getter, err := dhtindex.NewAnacrolixGetter(srv)
	if err != nil {
		return fmt.Errorf("engine: new anacrolix getter: %w", err)
	}
	e.lookup = dhtindex.NewLookup(getter)
	e.lookup.AddIndexer(e.identity.PublicKeyBytes(), "self")
	return nil
}

// dhtServer fishes the *dht.Server out of the anacrolix Client by
// type-asserting through the AnacrolixDhtServerWrapper. Returns nil
// if no anacrolix DHT server is registered (e.g. DisableDHT was set).
func (e *Engine) dhtServer() *dht.Server {
	for _, ds := range e.client.DhtServers() {
		if w, ok := ds.(torrent.AnacrolixDhtServerWrapper); ok {
			return w.Server
		}
	}
	return nil
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
	go e.ingestFileEvents(h)
	return h
}

// ingestFileEvents drains a Handle's FileEvents() channel and submits each
// completed file to the content-ingestion pipeline. Runs in a background
// goroutine so the tracker is never blocked waiting for extraction.
//
// Each FileInput closes over the Handle + FileIndex so the pipeline can
// lazily open a reader via t.Files()[i].NewReader() only when the
// extractor actually wants to read the bytes.
func (e *Engine) ingestFileEvents(h *Handle) {
	for ev := range h.FileEvents() {
		e.mu.Lock()
		p := e.pipeline
		closed := e.closed
		e.mu.Unlock()
		if p == nil || closed {
			continue
		}
		// Capture the file index by value for the closure.
		idx := ev.FileIndex
		in := indexer.FileInput{
			InfoHash:  ev.InfoHash,
			FileIndex: idx,
			Path:      ev.Path,
			Size:      ev.Size,
			OpenReader: func() (io.Reader, error) {
				files := h.T.Files()
				if idx < 0 || idx >= len(files) {
					return nil, errors.New("engine: file index out of range")
				}
				// anacrolix/torrent's Reader reads completed pieces from
				// the storage backend and blocks on incomplete ones. The
				// tracker guarantees the file is fully complete before we
				// get here, so reads should return eagerly.
				return files[idx].NewReader(), nil
			},
		}
		p.Submit(in)
	}
}

// autoIndex waits for a torrent's metadata to arrive and then indexes the
// torrent-level document into the attached *indexer.Index. If no index is
// attached, this is a no-op. Runs in a background goroutine; the waiting
// path is cancelled if Close is called on the engine.
//
// As a side effect, this also Submits a publish task to the dhtindex
// publisher (Layer D) so the torrent's keywords get pushed to the
// mainline DHT.
func (e *Engine) autoIndex(h *Handle) {
	select {
	case <-h.T.GotInfo():
	case <-time.After(5 * time.Minute):
		e.log.Warn("indexer.autoindex.timeout", "info_hash", h.T.InfoHash().HexString())
		return
	}

	e.mu.Lock()
	idx := e.idx
	pub := e.publisher
	closed := e.closed
	e.mu.Unlock()
	if closed {
		return
	}

	doc := indexerDocFromTorrent(h.T)

	if idx != nil {
		if err := idx.IndexTorrent(doc); err != nil {
			e.log.Warn("indexer.index_failed", "info_hash", doc.InfoHash, "err", err)
		} else {
			e.log.Info("indexer.indexed",
				"info_hash", doc.InfoHash,
				"name", doc.Name,
				"files", doc.FileCount,
				"size", doc.SizeBytes,
			)
		}
	}

	if pub != nil {
		ihBytes := h.T.InfoHash()
		pub.Submit(dhtindex.PublishTask{
			InfoHash:  ihBytes[:],
			Name:      doc.Name,
			FileCount: doc.FileCount,
			SizeBytes: doc.SizeBytes,
		})
	}
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
	if e.pipeline != nil {
		// Stop the pipeline first so in-flight extracts finish before the
		// underlying torrent.Client's storage shuts down underneath them.
		e.pipeline.Stop()
	}
	if e.publisher != nil {
		// Stop the publisher next so any in-flight Put traversal finishes
		// before the DHT server is torn down by Client.Close.
		e.publisher.Stop()
	}
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
