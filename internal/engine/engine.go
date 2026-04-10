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
	"github.com/swartznet/swartznet/internal/reputation"
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

	identity      *identity.Identity        // ed25519 publisher keypair, nil for tests
	publisher     *dhtindex.Publisher       // nil if no DHT or no identity
	manifest      *dhtindex.Manifest        // owned by publisher; nil iff publisher nil
	pointerPutter *dhtindex.AnacrolixPutter // shared by Publisher AND companion.Publisher; nil iff publisher nil
	lookup        *dhtindex.Lookup          // M4e DHT keyword lookup; nil iff no DHT
	bloom         *reputation.BloomFilter   // M5 known-good infohash filter; nil if disabled
	tracker       *reputation.Tracker       // M5 per-pubkey reputation tracker; nil if disabled
	sources       *reputation.SourceTracker // M9 per-hit source tracker; always non-nil after New

	mu       sync.Mutex
	closed   bool
	handles  map[metainfo.Hash]*Handle
	closeErr error
}

// Publisher returns the engine's DHT keyword publisher, or nil if
// the engine was constructed without one (no DHT, no identity, or a
// headless test setup).
func (e *Engine) Publisher() *dhtindex.Publisher { return e.publisher }

// PointerPutter returns the engine's BEP-46-style mutable-item
// putter, or nil if the engine has no DHT/identity. Returned as
// the concrete *AnacrolixPutter; the companion package only
// needs the PutInfohashPointer method, which the type already
// satisfies via its narrow PointerPutter interface. Used by the
// M11c companion publisher to publish a content-index pointer
// under the salt SaltContentIndex.
func (e *Engine) PointerPutter() *dhtindex.AnacrolixPutter { return e.pointerPutter }

// Identity returns the engine's persistent ed25519 keypair, or nil
// if no identity was loaded.
func (e *Engine) Identity() *identity.Identity { return e.identity }

// Lookup returns the engine's DHT keyword lookup handle, or nil if
// no DHT server is available. If we have an identity, our own pubkey
// is automatically added as a known indexer so we can find our own
// published entries during testing.
func (e *Engine) Lookup() *dhtindex.Lookup { return e.lookup }

// ReputationTracker returns the engine's per-pubkey reputation
// tracker, or nil if reputation is disabled.
func (e *Engine) ReputationTracker() *reputation.Tracker { return e.tracker }

// KnownGoodBloom returns the engine's known-good infohash Bloom
// filter, or nil if disabled.
func (e *Engine) KnownGoodBloom() *reputation.BloomFilter { return e.bloom }

// SourceTracker returns the engine's per-hit source tracker. The
// httpapi flag handler uses it to demote only the specific
// indexers that returned a flagged infohash. Always non-nil
// after engine.New (the tracker has no on-disk persistence; it
// repopulates naturally as the user runs queries).
func (e *Engine) SourceTracker() *reputation.SourceTracker { return e.sources }

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

	// pausedMu guards paused. anacrolix/torrent's
	// dataDownloadDisallowed field is private, so we mirror the
	// state here for the M10 GUI download controls.
	pausedMu sync.Mutex
	paused   bool
}

// IsPaused reports whether this torrent has been explicitly
// paused via Engine.PauseTorrent. Pause is currently a soft
// state — the torrent stays in the engine but stops requesting
// pieces and stops responding to peer requests.
func (h *Handle) IsPaused() bool {
	h.pausedMu.Lock()
	defer h.pausedMu.Unlock()
	return h.paused
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

	// Load the M5 spam-resistance state (Bloom filter + reputation
	// tracker) before the publisher / lookup so the lookup can be
	// wired up with both. Errors here are non-fatal — the node
	// still works for download + local search.
	if cfg.BloomPath != "" {
		if bf, err := reputation.LoadOrCreateBloom(cfg.BloomPath); err != nil {
			log.Warn("engine.bloom_load_err", "err", err)
		} else {
			eng.bloom = bf
			log.Info("engine.bloom_loaded",
				"path", cfg.BloomPath,
				"estimated_items", bf.EstimatedItems(),
			)
		}
	}
	if cfg.ReputationPath != "" {
		if tr, err := reputation.LoadOrCreateTracker(cfg.ReputationPath); err != nil {
			log.Warn("engine.reputation_load_err", "err", err)
		} else {
			eng.tracker = tr
			log.Info("engine.reputation_loaded", "path", cfg.ReputationPath)
		}
	}
	// SourceTracker (M9) has no on-disk persistence by design —
	// its content is the user's recent query history, which is
	// small and re-populates naturally on use. Constructing it
	// unconditionally keeps the targeted-flag path always
	// available even when the daemon was just restarted.
	eng.sources = reputation.NewSourceTracker(0)

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
	// Stash the putter so the M11c companion publisher can reuse
	// it for BEP-46 pointer puts. Same key, same DHT server.
	e.pointerPutter = put
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
	// Wire the M5 spam-resistance helpers if they were loaded,
	// plus the M9 source tracker (always present).
	if e.tracker != nil {
		e.lookup.SetTracker(e.tracker)
	}
	if e.bloom != nil {
		e.lookup.SetBloom(e.bloom)
	}
	if e.sources != nil {
		e.lookup.SetSourceTracker(e.sources)
	}
	if e.cfg.MinIndexerScore > 0 {
		e.lookup.SetMinIndexerScore(e.cfg.MinIndexerScore)
	}
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

// AddMagnetURI is the narrow adapter for httpapi.TorrentAdder. It
// wraps AddMagnet so the HTTP API can submit a magnet URI without
// depending on the full *Handle return type. Returns the
// 40-character lowercase hex infohash, parsed from the URI itself
// (so the call returns immediately; metadata fetch from the swarm
// continues asynchronously).
//
// Wraps the underlying AddMagnet in a recover() because
// anacrolix/torrent occasionally panics on pathological input
// (e.g. an all-zero infohash hits a defensive panicif.Zero check
// in client.AddTorrentOpt). The API must never crash the daemon
// over a malformed user input — every panic becomes a clean
// error returned to the HTTP handler, which then returns 400.
func (e *Engine) AddMagnetURI(uri string) (infohash string, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			infohash = ""
			err = fmt.Errorf("engine: AddMagnetURI panic: %v", rec)
		}
	}()
	h, err := e.AddMagnet(uri)
	if err != nil {
		return "", err
	}
	return h.T.InfoHash().HexString(), nil
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

// AddTorrentMetaInfo adds an in-memory metainfo to the engine and
// starts seeding/downloading it. Used by the F3 companion publisher
// (internal/companion) to seed the gzipped JSON content index it
// just built without doing a second disk read. Returns the same
// *Handle that AddTorrentFile would.
//
// Re-adding a metainfo with an infohash that is already known is a
// no-op at the anacrolix layer; this method returns the existing
// Handle in that case.
//
// Returning `any` instead of `*Handle` is a deliberate concession to
// the companion package, which only needs to know "this seeded ok"
// and would otherwise pull a hard import on internal/engine.
func (e *Engine) AddTorrentMetaInfo(mi *metainfo.MetaInfo) (any, error) {
	if mi == nil {
		return nil, errors.New("engine: nil metainfo")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, errors.New("engine: closed")
	}
	t, err := e.client.AddTorrent(mi)
	if err != nil {
		return nil, fmt.Errorf("engine: add torrent metainfo: %w", err)
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
	go e.autoConfirmOnComplete(h)
	return h
}

// autoConfirmOnComplete waits for a torrent to finish downloading
// and, if a known-good Bloom filter is wired in, adds the torrent's
// infohash to it. Successful downloads are the strongest possible
// "this hit was real" signal — every infohash a user actually fetches
// gets a permanent boost in future Layer-D queries.
//
// The goroutine exits if the engine is closed before the torrent
// completes.
func (e *Engine) autoConfirmOnComplete(h *Handle) {
	complete := h.T.Complete().On()
	select {
	case <-complete:
	case <-time.After(24 * time.Hour):
		// Long timeout: better to leak the goroutine than to hang
		// forever waiting on a torrent that never completes.
		return
	}

	e.mu.Lock()
	bf := e.bloom
	closed := e.closed
	e.mu.Unlock()
	if closed || bf == nil {
		return
	}

	ih := h.T.InfoHash()
	bf.Add(ih[:])
	e.log.Info("engine.bloom.auto_confirmed",
		"info_hash", ih.HexString(),
		"name", h.T.Name(),
	)
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

// TorrentSnapshot is the read-only view of a torrent's state for
// the GUI download list. Computed at call time from the underlying
// anacrolix torrent and the engine's pause-state mirror.
type TorrentSnapshot struct {
	// InfoHash is the 40-character lowercase hex form.
	InfoHash string
	// Name is the human-readable torrent name. Empty until the
	// metadata fetch completes.
	Name string
	// Size is the total bytes in the torrent. Zero until metadata.
	Size int64
	// BytesCompleted is the total bytes verified on disk so far.
	BytesCompleted int64
	// BytesMissing is the bytes still left to download.
	BytesMissing int64
	// Progress is the BytesCompleted / Size ratio in [0, 1]. Zero
	// when Size is unknown.
	Progress float64
	// Files is the count of files in the torrent.
	Files int
	// ActivePeers / HalfOpenPeers / PendingPeers / TotalPeers
	// mirror anacrolix's TorrentStats fields.
	ActivePeers   int
	HalfOpenPeers int
	PendingPeers  int
	TotalPeers    int
	// Seeders is the number of currently-connected peers that
	// have the entire torrent.
	Seeders int
	// Paused reports whether the user has paused this torrent
	// via PauseTorrent. While paused, no piece requests fly out
	// and no incoming requests are answered.
	Paused bool
	// Status is a human-readable summary of the torrent's
	// current state: "metadata", "downloading", "seeding",
	// "complete", or "paused".
	Status string
}

// TorrentSnapshots returns a TorrentSnapshot for every torrent
// currently in the engine. Cheap enough to call from a polling
// HTTP handler — each snapshot is a few field reads plus one
// anacrolix Stats() call.
func (e *Engine) TorrentSnapshots() []TorrentSnapshot {
	handles := e.Torrents()
	out := make([]TorrentSnapshot, 0, len(handles))
	for _, h := range handles {
		out = append(out, snapshotOf(h))
	}
	return out
}

// snapshotOf computes a TorrentSnapshot for a single Handle.
//
// Pre-metadata defensive: anacrolix/torrent v1.61.0 panics with a
// nil pointer dereference inside BytesMissing()/bytesLeft() when
// the torrent has not yet received its info dictionary. We
// detect that case via t.Info() == nil and skip every call that
// would touch the (still-nil) Info. The HTTP handler's
// net/http panic recovery would otherwise leave the response
// empty and the client would see "empty reply from server".
//
// Files() / Length() / BytesCompleted() / BytesMissing() / Stats()
// all need the info; only InfoHash() / Name() / IsPaused() are
// safe pre-metadata.
func snapshotOf(h *Handle) TorrentSnapshot {
	t := h.T
	ih := t.InfoHash().HexString()
	paused := h.IsPaused()

	if t.Info() == nil {
		status := "metadata"
		if paused {
			status = "paused"
		}
		return TorrentSnapshot{
			InfoHash: ih,
			Name:     t.Name(),
			Paused:   paused,
			Status:   status,
		}
	}

	stats := t.Stats()
	size := t.Length()
	completed := t.BytesCompleted()
	missing := t.BytesMissing()
	files := len(t.Files())

	var progress float64
	if size > 0 {
		progress = float64(completed) / float64(size)
		if progress > 1 {
			progress = 1
		}
	}

	status := "downloading"
	switch {
	case paused:
		status = "paused"
	case missing == 0 && size > 0:
		status = "seeding"
	case completed > 0:
		status = "downloading"
	}

	return TorrentSnapshot{
		InfoHash:       ih,
		Name:           t.Name(),
		Size:           size,
		BytesCompleted: completed,
		BytesMissing:   missing,
		Progress:       progress,
		Files:          files,
		ActivePeers:    stats.ActivePeers,
		HalfOpenPeers:  stats.HalfOpenPeers,
		PendingPeers:   stats.PendingPeers,
		TotalPeers:     stats.TotalPeers,
		Seeders:        stats.ConnectedSeeders,
		Paused:         paused,
		Status:         status,
	}
}

// PauseTorrent disables data download for the torrent identified
// by the 40-char hex infohash. Idempotent. Returns an error if
// the infohash is unknown to the engine.
//
// Pause is a soft stop: the torrent stays registered with the
// engine, peer connections stay open for sn_search, but no
// piece requests fly out. Calling ResumeTorrent later restores
// normal operation without re-fetching metadata.
func (e *Engine) PauseTorrent(infoHashHex string) error {
	h, err := e.handleByHex(infoHashHex)
	if err != nil {
		return err
	}
	h.pausedMu.Lock()
	already := h.paused
	h.paused = true
	h.pausedMu.Unlock()
	if already {
		return nil
	}
	h.T.DisallowDataDownload()
	h.T.DisallowDataUpload()
	e.log.Info("engine.torrent_paused", "info_hash", infoHashHex)
	return nil
}

// ResumeTorrent re-enables data download/upload for the torrent.
// Idempotent. No-op if the torrent is already running.
func (e *Engine) ResumeTorrent(infoHashHex string) error {
	h, err := e.handleByHex(infoHashHex)
	if err != nil {
		return err
	}
	h.pausedMu.Lock()
	wasPaused := h.paused
	h.paused = false
	h.pausedMu.Unlock()
	if !wasPaused {
		return nil
	}
	h.T.AllowDataDownload()
	h.T.AllowDataUpload()
	e.log.Info("engine.torrent_resumed", "info_hash", infoHashHex)
	return nil
}

// RemoveTorrent drops the torrent from the engine entirely. The
// underlying anacrolix Torrent is dropped (peer connections
// closed, piece state forgotten); on-disk file content under
// DataDir is left in place so the user can reuse it. The
// associated index entries are NOT deleted — call
// indexer.DeleteContentForTorrent if you want to forget the
// indexed content as well.
func (e *Engine) RemoveTorrent(infoHashHex string) error {
	h, err := e.handleByHex(infoHashHex)
	if err != nil {
		return err
	}
	// Tear down our subscriptions before dropping the underlying
	// torrent so the goroutines exit cleanly.
	h.pieceSub.Close()
	h.fileSub.Close()
	h.T.Drop()

	e.mu.Lock()
	delete(e.handles, h.T.InfoHash())
	e.mu.Unlock()
	e.log.Info("engine.torrent_removed", "info_hash", infoHashHex)
	return nil
}

// handleByHex parses a 40-char hex infohash and looks up the
// matching Handle. Returns a descriptive error if the input is
// malformed or the infohash is not registered.
func (e *Engine) handleByHex(infoHashHex string) (*Handle, error) {
	var ih metainfo.Hash
	if err := ih.FromHexString(infoHashHex); err != nil {
		return nil, fmt.Errorf("engine: invalid infohash %q: %w", infoHashHex, err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	h, ok := e.handles[ih]
	if !ok {
		return nil, fmt.Errorf("engine: no torrent with infohash %s", infoHashHex)
	}
	return h, nil
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
	// Persist the spam-resistance state to disk before shutdown
	// so reputation and known-good entries survive across runs.
	if e.bloom != nil {
		if err := e.bloom.Save(); err != nil {
			e.log.Warn("engine.bloom_save_err", "err", err)
		}
	}
	if e.tracker != nil {
		if err := e.tracker.Save(); err != nil {
			e.log.Warn("engine.reputation_save_err", "err", err)
		}
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
