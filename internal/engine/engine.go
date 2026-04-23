package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/anacrolix/dht/v2"
	peer_store "github.com/anacrolix/dht/v2/peer-store"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	pp "github.com/anacrolix/torrent/peer_protocol"
	"golang.org/x/time/rate"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/dhtindex"
	"github.com/swartznet/swartznet/internal/identity"
	"github.com/swartznet/swartznet/internal/indexer"
	"github.com/swartznet/swartznet/internal/reputation"
	"github.com/swartznet/swartznet/internal/signing"
	"github.com/swartznet/swartznet/internal/swarmsearch"
	"github.com/swartznet/swartznet/internal/trust"
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
	pointerGetter *dhtindex.AnacrolixGetter // shared by Lookup AND companion.Subscriber; nil iff no DHT
	lookup        *dhtindex.Lookup          // M4e DHT keyword lookup; nil iff no DHT
	bloom         *reputation.BloomFilter   // M5 known-good infohash filter; nil if disabled
	tracker       *reputation.Tracker       // M5 per-pubkey reputation tracker; nil if disabled
	sources       *reputation.SourceTracker // M9 per-hit source tracker; always non-nil after New
	trust         *trust.Store              // v0.5 publisher trust list; nil if disabled

	ulLimiter *rate.Limiter // upload rate limiter; rate.Inf when unlimited
	dlLimiter *rate.Limiter // download rate limiter; rate.Inf when unlimited

	maxActiveDownloads int   // 0 = unlimited (default). See queue.go.
	nextQueueOrder     int64 // monotonic counter assigned to each new Handle

	// sess persists the list of open torrents and their state
	// (paused / indexing / queue order) to <DataDir>/session.json
	// so a daemon restart re-adds every torrent. Always non-nil
	// after engine.New, but its persistence path is empty when
	// cfg.DataDir is empty (in-memory-only mode for tests).
	sess *session

	// bgCtx is cancelled on Engine.Close so background goroutines
	// launched from the engine (e.g. the post-add VerifyData rehash)
	// can bail out promptly instead of racing with client shutdown.
	bgCtx    context.Context
	bgCancel context.CancelFunc

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

// PointerGetter returns the engine's BEP-46-style mutable-item
// getter, or nil if the engine has no DHT. Mirrors PointerPutter
// for the read side. The M11d companion subscriber uses it to
// resolve content-index pointers published by other nodes.
func (e *Engine) PointerGetter() *dhtindex.AnacrolixGetter { return e.pointerGetter }

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

// TrustStore returns the publisher-trust list, or nil if
// trust-list loading failed or is disabled in the config.
func (e *Engine) TrustStore() *trust.Store { return e.trust }

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

	// indexMu guards indexing. When true (the default), autoIndex
	// and ingestFileEvents feed this torrent's metadata and file
	// contents into the attached *indexer.Index. When false, both
	// paths skip silently. Toggle via Engine.SetTorrentIndexing.
	indexMu  sync.Mutex
	indexing bool

	// queueMu guards queued and queueOrder. A handle is "queued"
	// when the engine's MaxActiveDownloads cap would be exceeded
	// if this torrent started downloading. Queued torrents keep
	// metadata fetch + indexing but do NOT flip their files to
	// Normal priority until a slot opens up. queueOrder controls
	// promotion order — lower values promote first. See queue.go.
	queueMu    sync.Mutex
	queued     bool
	queueOrder int64

	// signedBy is the 64-char lowercase hex form of the ed25519
	// public key that signed this torrent's .torrent file, or
	// empty if the torrent was unsigned or verification failed.
	// Populated at add-time by AddTorrentFile; magnet/infohash
	// adds always leave this empty.
	signedBy string

	// rateMu guards the transfer-rate sampling state. snapshotOf
	// computes per-second download/upload rate by subtracting the
	// previous reading's useful-data counts over wall-clock
	// delta, so the GUI can show live throughput without us
	// polling at a higher cadence than the GUI itself.
	rateMu           sync.Mutex
	lastRateSampleAt time.Time
	lastBytesRead    int64
	lastBytesWritten int64
	lastDownloadRate int64
	lastUploadRate   int64
}

// SignedBy returns the ed25519 public key (64-char hex) that
// signed this torrent's .torrent file, or empty string if the
// torrent was unsigned, verification failed, or the torrent was
// added via magnet / raw infohash. Read-only: set at add time.
func (h *Handle) SignedBy() string { return h.signedBy }

// IsQueued reports whether this handle is currently waiting for a
// download slot. See Engine.SetMaxActiveDownloads.
func (h *Handle) IsQueued() bool {
	h.queueMu.Lock()
	defer h.queueMu.Unlock()
	return h.queued
}

// sampleRate reads the current anacrolix ConnStats, subtracts the
// previous reading to derive a per-second download/upload rate,
// updates the cached state, and returns the fresh rates in
// bytes/sec.
//
// Returns zeros on the first call (no previous reading to compare
// against), and on calls that happen too close together (<100ms)
// we return the last computed rate rather than a noisy value.
// The expected call cadence is the GUI's 2-second poll; at that
// cadence the rate samples stay useful and don't flicker.
func (h *Handle) sampleRate() (downBPS, upBPS int64) {
	if h.T.Info() == nil {
		return 0, 0
	}
	stats := h.T.Stats()
	now := time.Now()
	readNow := stats.BytesReadUsefulData.Int64()
	writeNow := stats.BytesWrittenData.Int64()

	h.rateMu.Lock()
	defer h.rateMu.Unlock()

	prevAt := h.lastRateSampleAt
	if prevAt.IsZero() {
		// First reading — seed state, return zeros.
		h.lastRateSampleAt = now
		h.lastBytesRead = readNow
		h.lastBytesWritten = writeNow
		return 0, 0
	}

	elapsed := now.Sub(prevAt)
	if elapsed < 100*time.Millisecond {
		return h.lastDownloadRate, h.lastUploadRate
	}

	elapsedSec := elapsed.Seconds()
	dr := int64(float64(readNow-h.lastBytesRead) / elapsedSec)
	ur := int64(float64(writeNow-h.lastBytesWritten) / elapsedSec)
	if dr < 0 {
		dr = 0 // stats counters never go backwards, but be defensive
	}
	if ur < 0 {
		ur = 0
	}

	h.lastRateSampleAt = now
	h.lastBytesRead = readNow
	h.lastBytesWritten = writeNow
	h.lastDownloadRate = dr
	h.lastUploadRate = ur
	return dr, ur
}

func (h *Handle) setQueued(v bool) {
	h.queueMu.Lock()
	h.queued = v
	h.queueMu.Unlock()
}

// IsIndexing reports whether this torrent is currently eligible
// for automatic indexing (torrent-level and content-level). The
// default for every newly-added torrent is true; set it to false
// via Engine.SetTorrentIndexing before files complete if you want
// to opt a specific torrent out.
func (h *Handle) IsIndexing() bool {
	h.indexMu.Lock()
	defer h.indexMu.Unlock()
	return h.indexing
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
//
// Every call to FileEvents() creates an independent subscription via the
// underlying fileTracker. Callers MUST bind the result to a local variable
// and then receive from that variable — evaluating h.FileEvents() inside a
// for/select loop allocates a new, empty channel on every iteration and the
// loop will hang forever. Use SubscribeFileEvents() for new code.
func (h *Handle) FileEvents() <-chan FileCompleteEvent {
	return h.fileSub.Subscribe()
}

// SubscribeFileEvents is the preferred alias for FileEvents and makes the
// per-call subscription semantics explicit at the call site. Each caller
// receives every emitted event independently; the tracker fans out to all
// subscribers so two consumers (e.g. the CLI progress loop and the ingest
// pipeline) no longer race for the same single channel.
func (h *Handle) SubscribeFileEvents() <-chan FileCompleteEvent {
	return h.fileSub.Subscribe()
}

// New constructs an Engine with the given config. The config is validated and
// the data directory is created if missing. The underlying Client is started
// in the background (it listens for peers and joins the DHT if enabled).
func New(ctx context.Context, cfg config.Config, log *slog.Logger) (*Engine, error) {
	// ctx is used for the M16e feeler goroutine lifecycle.
	// Cancelling it stops the feeler alongside the engine.
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

	// Give the embedded DHT server a PeerStore. Without one, the
	// anacrolix server refuses to issue write tokens in its
	// get_peers replies (server.go: it only fills r.Token when
	// config.PeerStore is non-nil). That breaks BEP-5 compliance —
	// any vanilla client doing the canonical get_peers →
	// announce_peer sequence stalls on the second hop because our
	// reply had no Token to echo back.
	//
	// We plug in peer_store.InMemory, the library's default. This
	// is the shared instance the DHT hands to every call path,
	// which means vanilla announce_peer queries correctly
	// populate our local per-infohash peer table too.
	peerStore := &peer_store.InMemory{}
	bootstrapAddrs := append([]string(nil), cfg.DHTBootstrapAddrs...)
	dhtInsecure := cfg.DHTInsecure
	tc.ConfigureAnacrolixDhtServer = func(sc *dht.ServerConfig) {
		if sc.PeerStore == nil {
			sc.PeerStore = peerStore
		}
		// If the caller explicitly pre-seeded the DHT's bootstrap
		// node list, honour it instead of anacrolix's public
		// mainline defaults (router.bittorrent.com etc.). Used by
		// the Layer-B testbed where default bootstrap hosts are
		// unreachable from the isolated docker bridge. Empty =
		// defaults.
		if len(bootstrapAddrs) > 0 {
			snapshot := append([]string(nil), bootstrapAddrs...)
			sc.StartingNodes = func() ([]dht.Addr, error) {
				return dht.ResolveHostPorts(snapshot)
			}
		}
		// Opt out of BEP-42 node-ID security enforcement when the
		// caller explicitly asked for it. Required for private
		// DHTs on docker bridges / k8s cluster IPs, where node
		// IDs can never pass the BEP-42 "tied to your public IP"
		// check and puts therefore silently filter out every
		// target as "not secure". See
		// internal/config/config.go:DHTInsecure for why operators
		// must never set this on mainline.
		if dhtInsecure {
			sc.NoSecurity = true
		}
	}

	// Install mutable rate limiters so Engine.SetUploadLimit /
	// SetDownloadLimit can tune bandwidth at runtime without
	// restarting the Client. Default: unlimited. We keep a
	// reference on the Engine so the mutator methods can
	// SetLimit/SetBurst at runtime.
	//
	// Burst must be positive even in "unlimited" mode because
	// anacrolix's openNewConns path refuses to dial when the
	// DownloadRateLimiter reports Tokens() <= 0. See ratelimit.go.
	ulLimiter := rate.NewLimiter(rate.Inf, unlimitedBurst)
	dlLimiter := rate.NewLimiter(rate.Inf, unlimitedBurst)
	tc.UploadRateLimiter = ulLimiter
	tc.DownloadRateLimiter = dlLimiter

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
	//
	// CRITICAL: anacrolix's mainReadLoop holds the client lock while
	// dispatching this callback (peerconn.go mainReadLoop). Two
	// things go wrong if we handle the message synchronously:
	//
	//  1. Deadlock: handleQuery eventually calls reply(), which
	//     invokes pc.WriteExtendedMessage, which tries to re-acquire
	//     the same client lock — self-deadlock on the read-loop
	//     goroutine. The entire client freezes.
	//  2. Performance: handleQuery runs a Bleve search synchronously
	//     before producing a reply. That can take many milliseconds
	//     or more on a large index. While it runs, the client lock
	//     is held, which means NO peer on this client can send or
	//     receive ANY message — one slow query stalls the whole
	//     peer set.
	//
	// The fix: dispatch the entire HandleMessage call to a goroutine.
	// The callback returns immediately, the read loop releases the
	// lock, and sn_search work runs entirely off the critical path.
	// The payload slice is copied because anacrolix reuses the
	// decoder's buffer across messages.
	swarmLog := log
	tc.Callbacks.PeerConnReadExtensionMessage = append(
		tc.Callbacks.PeerConnReadExtensionMessage,
		func(ev torrent.PeerConnReadExtensionMessageEvent) {
			name, _, err := ev.PeerConn.LocalLtepProtocolMap.LookupId(ev.ExtensionNumber)
			if err != nil || name != swarmsearch.ExtensionName {
				return
			}
			peerAddr := ev.PeerConn.RemoteAddr.String()
			pc := ev.PeerConn
			// Copy the payload: anacrolix's decoder buffer can be
			// overwritten by the next message once we return from
			// this callback.
			payload := append([]byte(nil), ev.Payload...)
			go func() {
				reply := func(body []byte) error {
					// Spawn ANOTHER goroutine for the write so
					// the HandleMessage code path never blocks
					// on the client lock if multiple writes
					// queue up.
					bodyCopy := append([]byte(nil), body...)
					go func() {
						if err := pc.WriteExtendedMessage(swarmsearch.ExtensionName, bodyCopy); err != nil {
							swarmLog.Debug("engine.swarm.reply_err",
								"peer", peerAddr, "err", err)
						}
					}()
					return nil
				}
				swarm.HandleMessage(peerAddr, payload, reply)
			}()
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
	if cfg.Regtest {
		// Warn-level so it cannot be missed in operator logs —
		// a real node running regtest mode would hammer the
		// mainline DHT and get rate-limited into the ground.
		// The bitcoin-lessons doc specifically calls out that
		// regtest must be impossible to mistake for production.
		log.Warn("engine.regtest_mode_active",
			"warning", "DO NOT USE IN PRODUCTION",
			"reason", "accelerated publisher / companion timings would be detected as abuse on mainnet",
		)
	}

	eng := &Engine{
		cfg:       cfg,
		client:    cl,
		log:       log,
		swarm:     swarm,
		handles:   make(map[metainfo.Hash]*Handle),
		peers:     peers,
		ulLimiter: ulLimiter,
		dlLimiter: dlLimiter,
	}
	eng.bgCtx, eng.bgCancel = context.WithCancel(context.Background())
	// Hand the peer tracker to the swarmSender so Query fan-out can
	// find specific peers by address. The callbacks above and this
	// sender share the same peerTracker instance.
	swarm.SetSender(&swarmSender{peers: peers})
	// M16e: start the feeler goroutine that periodically probes
	// random "new" peers to promote them to "tried". Mirrors
	// Bitcoin Core's feeler connection pattern. The goroutine
	// runs until ctx is cancelled (engine shutdown).
	feelerInterval := swarmsearch.FeelerIntervalProd
	if cfg.Regtest {
		feelerInterval = swarmsearch.FeelerIntervalRegtest
	}
	swarm.StartFeeler(ctx, feelerInterval)

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
			// M13c: import the curated seed list on top of the
			// loaded reputation state. The seed list is the
			// bootstrap for the cold-start reputation network —
			// every fresh install inherits ~20 maintainer pubkeys
			// with a decaying +0.45 score bonus, letting the
			// network function on day one. Missing file is fine;
			// parse errors are logged but do not abort.
			if cfg.SeedListPath != "" {
				n, errs := tr.LoadSeedList(cfg.SeedListPath)
				if n > 0 {
					log.Info("engine.seed_list_loaded",
						"path", cfg.SeedListPath,
						"imported", n,
					)
				}
				for _, e := range errs {
					log.Warn("engine.seed_list_err", "err", e)
				}
			}
		}
	}
	// SourceTracker (M9) has no on-disk persistence by design —
	// its content is the user's recent query history, which is
	// small and re-populates naturally on use. Constructing it
	// unconditionally keeps the targeted-flag path always
	// available even when the daemon was just restarted.
	eng.sources = reputation.NewSourceTracker(0)

	// Load (or create) the publisher trust list. Failures are
	// non-fatal: a daemon without a trust list still works, it
	// just can't auto-confirm downloads from signed publishers.
	if cfg.TrustPath != "" {
		if ts, err := trust.LoadOrCreate(cfg.TrustPath); err != nil {
			log.Warn("engine.trust_load_err", "err", err)
		} else {
			eng.trust = ts
			log.Info("engine.trust_loaded",
				"path", cfg.TrustPath,
				"entries", len(ts.List()),
			)
		}
	}

	// Load the on-disk session manifest so RestoreSession (called
	// by daemon.New right after we return) can re-add every torrent
	// the user had open last time. A missing file is normal — fresh
	// installs start with no torrents. Corrupt manifests are logged
	// and treated as empty; we never fail engine.New over session.
	sess, err := loadSession(cfg.DataDir)
	if err != nil {
		log.Warn("engine.session_load_err", "err", err)
		sess = &session{entries: make(map[string]sessionEntry)}
	}
	eng.sess = sess
	if n := len(sess.list()); n > 0 {
		log.Info("engine.session_loaded", "path", sess.path, "entries", n)
	}

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
	// it for BEP-46 pointer puts — this happens even in leech-
	// only mode (DisableDHTPublish) because companion pointers are
	// the user's own opt-in publication and are governed by the
	// separate CompanionDir / follow-list plumbing, not by this
	// knob. The keyword Publisher worker below is what
	// DisableDHTPublish actually suppresses.
	e.pointerPutter = put

	if e.cfg.DisableDHTPublish {
		// M13d: skip the keyword Publisher worker but keep the
		// lookup path below intact so the node can still subscribe
		// to other publishers and fetch companion indexes. This is
		// the "leech-only DHT" privacy mode.
		e.log.Info("engine.publisher_disabled_by_config",
			"reason", "cfg.DisableDHTPublish",
		)
	} else {
		mf, err := dhtindex.LoadOrCreateManifest(e.cfg.PublisherManifest)
		if err != nil {
			return fmt.Errorf("engine: load publisher manifest: %w", err)
		}
		e.manifest = mf
		// M15a: regtest mode swaps in accelerated time
		// constants so scenario tests run in seconds instead
		// of hours.
		pubOpts := dhtindex.DefaultPublisherOptions()
		if e.cfg.Regtest {
			pubOpts = dhtindex.RegtestPublisherOptions()
		}
		e.publisher = dhtindex.NewPublisher(put, mf, pubOpts, e.log)
		e.publisher.Start()
		e.log.Info("engine.publisher_started",
			"manifest", e.cfg.PublisherManifest,
			"refresh_interval", pubOpts.RefreshInterval.String(),
		)
	}

	// Build the matching lookup handle. Self-pubkey is added as a
	// known indexer so the user can `swartznet search --dht` against
	// their own freshly-published entries during local testing.
	getter, err := dhtindex.NewAnacrolixGetter(srv)
	if err != nil {
		return fmt.Errorf("engine: new anacrolix getter: %w", err)
	}
	// Stash the getter so the M11d companion subscriber can
	// reuse it for BEP-46 pointer gets.
	e.pointerGetter = getter
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

	// Wire-compat §8.4-C: hand our publisher identity to the
	// sn_search protocol so outbound PeerAnnounce frames carry
	// `pk`, and install a sink that feeds gossip-discovered
	// pubkeys back into the lookup's known-indexer set.
	e.swarm.SetPublisherPubkey(e.identity.PublicKey)
	e.swarm.SetIndexerSink(&gossipIndexerSink{lookup: e.lookup})
	// Flip caps.Publisher to 1 so outgoing PeerAnnounce frames
	// actually include the `pk` field (the gossip path in
	// swarmsearch.Protocol.onRemoteHandshake gates on
	// `caps.Publisher > 0`, not on publisherPubkey being non-
	// nil). Without this, every running publisher's pubkey stays
	// invisible to peers and Layer-D cross-registration never
	// fires. Latent bug since the gossip feature shipped in
	// wire-compat row 8.4-C — first caught by the Layer-B
	// s12 scenario, where leech-1's /search --dht found
	// indexers_asked=0 because nothing gossiped.
	//
	// If DisableDHTPublish is set we're running in "leech-only
	// DHT" mode: the Publisher worker is suppressed above but
	// the node still has an identity and still lookups via the
	// DHT. Keeping Publisher=0 in that mode is intentional —
	// we don't want passive nodes gossiping themselves as
	// indexers when they aren't actually pushing entries.
	if !e.cfg.DisableDHTPublish {
		currentCaps := e.swarm.Capabilities()
		currentCaps.Publisher = 1
		e.swarm.SetCapabilities(currentCaps)
	}
	return nil
}

// gossipIndexerSink adapts *dhtindex.Lookup to the
// swarmsearch.IndexerSink interface. NoteGossipIndexer is a
// plain AddIndexer (idempotent: re-adds update the label only).
type gossipIndexerSink struct {
	lookup *dhtindex.Lookup
}

func (s *gossipIndexerSink) NoteGossipIndexer(pubkey [32]byte, label string) {
	if s == nil || s.lookup == nil {
		return
	}
	s.lookup.AddIndexer(pubkey, label)
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
//
// The magnet is parsed locally first so invalid or zero-infohash URIs fail
// fast with a descriptive error. Without that check, anacrolix/torrent's
// AddTorrentOpt panics on a zero infohash, which on a long-running daemon
// would tear down every subsystem via the caller's deferred Close.
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
		return nil, errors.New("engine: closed")
	}
	t, err := e.client.AddMagnet(uri)
	if err != nil {
		e.mu.Unlock()
		return nil, fmt.Errorf("engine: add magnet: %w", err)
	}
	h := e.registerLocked(t)
	e.mu.Unlock()
	e.persistAdd(h, "magnet", uri, "")
	return h, nil
}

// AddTorrentFile loads a .torrent file from disk and adds it to the swarm.
func (e *Engine) AddTorrentFile(path string) (*Handle, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("engine: read .torrent: %w", err)
	}
	mi, err := metainfo.Load(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("engine: load .torrent: %w", err)
	}

	// Verify the SwartzNet publisher signature, if present.
	// ErrNotSigned is normal — most .torrent files in the world
	// are unsigned. ErrBadSignature is rare but worth flagging
	// in the log so the user sees why their signed torrent
	// didn't show a verified publisher.
	var signedBy string
	if sig, verr := signing.VerifyBytes(raw); verr == nil {
		signedBy = sig.PubKeyHex()
		e.log.Info("engine.torrent_signature_verified",
			"path", path,
			"pubkey", signedBy,
		)
	} else if !errors.Is(verr, signing.ErrNotSigned) {
		e.log.Warn("engine.torrent_signature_rejected",
			"path", path,
			"err", verr,
		)
	}

	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, errors.New("engine: closed")
	}
	t, err := e.client.AddTorrent(mi)
	if err != nil {
		e.mu.Unlock()
		return nil, fmt.Errorf("engine: add torrent: %w", err)
	}
	h := e.registerLocked(t)
	if signedBy != "" {
		h.signedBy = signedBy
	}
	e.mu.Unlock()

	// Persist a copy of the original .torrent so RestoreSession
	// can re-add by file even if the user later moves or deletes
	// the source path. Preserves snet.* signing fields.
	ihHex := h.T.InfoHash().HexString()
	tname, werr := e.sess.writeTorrentCopy(ihHex, raw)
	if werr != nil {
		e.log.Warn("engine.session_torrent_copy_err", "err", werr)
	}
	e.persistAdd(h, "file", "", tname)
	return h, nil
}

// AddInfoHash adds a torrent by raw 20-byte infohash. Equivalent
// to constructing a magnet URI with no display name and no
// trackers and calling AddMagnet, but skips the URI parse step.
// The returned Handle's metadata fetch happens asynchronously
// over the swarm — the caller must wait on T.GotInfo() before
// inspecting the file list.
//
// Used by the M11d companion subscriber to fetch a content-index
// torrent given only the infohash from a BEP-46 pointer.
func (e *Engine) AddInfoHash(infoHash [20]byte) (*Handle, error) {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, errors.New("engine: closed")
	}
	t, _ := e.client.AddTorrentInfoHash(metainfo.Hash(infoHash))
	h := e.registerLocked(t)
	e.mu.Unlock()
	e.persistAdd(h, "infohash", "", "")
	return h, nil
}

// FetchCompanionTorrent satisfies companion.CompanionFetcher. It
// adds the torrent identified by infohash to the engine, waits
// for metadata to arrive over the swarm, asks the engine to
// download every piece, then blocks until the (single) file
// inside is fully downloaded. Returns the absolute on-disk path.
//
// Multi-file companion torrents are rejected — the M11 format
// puts everything inside one gzipped JSON file, so a multi-file
// torrent indicates either a malformed publisher or an attempt
// to slip a non-companion torrent past the subscriber.
//
// ctx cancellation aborts the wait but does NOT remove the
// torrent from the engine; subsequent retries can reuse the
// existing handle.
func (e *Engine) FetchCompanionTorrent(ctx context.Context, infoHash [20]byte) (string, error) {
	h, err := e.AddInfoHash(infoHash)
	if err != nil {
		return "", err
	}

	// Wait for metadata.
	select {
	case <-h.T.GotInfo():
	case <-ctx.Done():
		return "", ctx.Err()
	}

	files := h.T.Files()
	if len(files) != 1 {
		return "", fmt.Errorf("engine: companion torrent has %d files, want exactly 1", len(files))
	}
	target := files[0]
	target.Download()
	h.T.DownloadAll()

	// Wait for the file to fully download. Poll every 250 ms;
	// anacrolix does not expose a per-file completion channel
	// in this version, so we synthesise one with BytesCompleted.
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		if target.BytesCompleted() >= target.Length() {
			break
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-tick.C:
		}
	}

	// Build the absolute path: anacrolix puts the file at
	// DataDir/<torrent name>/<file path> for multi-file torrents
	// and DataDir/<file path> for single-file ones.
	// torrent.File.Path() gives the path relative to the torrent
	// root, so the full path is DataDir + (torrent name iff
	// multi-file) + relative path. For our companion torrents we
	// always have exactly one file, so the layout is:
	// DataDir/<info.Name>.
	info := h.T.Info()
	if info == nil {
		// We waited for GotInfo above, so this is paranoid.
		return "", errors.New("engine: companion torrent has no info after GotInfo")
	}
	return filepath.Join(e.cfg.DataDir, info.Name), nil
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
	if e.closed {
		e.mu.Unlock()
		return nil, errors.New("engine: closed")
	}
	t, err := e.client.AddTorrent(mi)
	if err != nil {
		e.mu.Unlock()
		return nil, fmt.Errorf("engine: add torrent metainfo: %w", err)
	}
	h := e.registerLocked(t)
	e.mu.Unlock()
	// Every caller of AddTorrentMetaInfo is seeding freshly-written
	// content: the create-torrent flow hashed the user's local
	// files moments ago, and the companion publisher just wrote a
	// gzipped JSON index next to the data dir. In both cases the
	// pieces are already on disk, but anacrolix does not verify
	// them eagerly — it waits for a peer request before touching
	// storage. With no peers yet (brand-new infohash), that wait
	// is forever and the download progress bar stays at 0% until
	// someone else joins the swarm. Kick off VerifyData in the
	// background so the bar reflects the real state within the
	// time it takes to rehash the local copy; piece-complete
	// events fire as each piece passes, which feeds the file
	// tracker and the indexer pipeline the same way a real
	// download would.
	go func() {
		if err := t.VerifyDataContext(e.bgCtx); err != nil && e.bgCtx.Err() == nil {
			e.log.Debug("engine.verify_data_err", "err", err)
		}
	}()
	return h, nil
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
	e.nextQueueOrder++
	h := &Handle{
		T:          t,
		pieceSub:   startPieceSubscription(t, e.log),
		fileSub:    startFileTracker(t, e.log),
		indexing:   true,
		queueOrder: e.nextQueueOrder,
	}
	e.handles[ih] = h
	go e.autoDownload(h)
	go e.autoIndex(h)
	go e.ingestFileEvents(h)
	go e.autoConfirmOnComplete(h)
	go e.upgradeMagnetSession(h)
	return h
}

// persistAdd records a freshly-added torrent in the on-disk session
// manifest. Called by AddMagnet/AddTorrentFile/AddInfoHash after the
// engine lock has been released.
func (e *Engine) persistAdd(h *Handle, addedVia, magnetURI, torrentFile string) {
	if e.sess == nil {
		return
	}
	h.queueMu.Lock()
	qo := h.queueOrder
	h.queueMu.Unlock()
	h.indexMu.Lock()
	indexing := h.indexing
	h.indexMu.Unlock()
	h.pausedMu.Lock()
	paused := h.paused
	h.pausedMu.Unlock()
	ih := h.T.InfoHash().HexString()
	if err := e.sess.update(ih, func(entry *sessionEntry) {
		entry.AddedVia = addedVia
		// Only overwrite source fields when supplied so an
		// upgradeMagnetSession that fires after persistAdd doesn't
		// clobber the file copy with empty strings.
		if magnetURI != "" {
			entry.MagnetURI = magnetURI
		}
		if torrentFile != "" {
			entry.TorrentFile = torrentFile
		}
		entry.Indexing = indexing
		entry.Paused = paused
		entry.QueueOrder = qo
		if h.signedBy != "" {
			entry.SignedBy = h.signedBy
		}
	}); err != nil {
		e.log.Warn("engine.session_update_err", "err", err)
	}
}

// persistState updates only the mutable state fields (paused,
// indexing) for an existing entry. Called by Pause/Resume/SetIndexing.
func (e *Engine) persistState(h *Handle) {
	if e.sess == nil {
		return
	}
	h.pausedMu.Lock()
	paused := h.paused
	h.pausedMu.Unlock()
	h.indexMu.Lock()
	indexing := h.indexing
	h.indexMu.Unlock()
	h.queueMu.Lock()
	qo := h.queueOrder
	h.queueMu.Unlock()
	ih := h.T.InfoHash().HexString()
	if err := e.sess.update(ih, func(entry *sessionEntry) {
		entry.Paused = paused
		entry.Indexing = indexing
		entry.QueueOrder = qo
	}); err != nil {
		e.log.Warn("engine.session_update_err", "err", err)
	}
}

// upgradeMagnetSession waits for metadata to arrive on a magnet-
// added torrent, then dumps the in-memory metainfo to the
// torrents/ dir and upgrades the session entry to AddedVia="file".
// On the next restart the engine can re-add by file directly,
// skipping the ut_metadata fetch round trip.
//
// No-op for already-file entries, sessionless engines, and
// torrents whose metadata never arrives.
func (e *Engine) upgradeMagnetSession(h *Handle) {
	if e.sess == nil {
		return
	}
	select {
	case <-h.T.GotInfo():
	case <-time.After(10 * time.Minute):
		return
	}
	ih := h.T.InfoHash().HexString()
	e.sess.mu.Lock()
	entry, ok := e.sess.entries[ih]
	already := ok && entry.TorrentFile != ""
	e.sess.mu.Unlock()
	if already {
		return
	}
	mi := h.T.Metainfo()
	miBytes, err := bencode.Marshal(mi)
	if err != nil {
		e.log.Warn("engine.session_metainfo_marshal_err", "info_hash", ih, "err", err)
		return
	}
	tname, err := e.sess.writeTorrentCopy(ih, miBytes)
	if err != nil {
		e.log.Warn("engine.session_torrent_copy_err", "info_hash", ih, "err", err)
		return
	}
	if err := e.sess.update(ih, func(entry *sessionEntry) {
		entry.TorrentFile = tname
		// Keep AddedVia as "magnet" or "infohash" — the next restore
		// will prefer TorrentFile if set, but the original AddedVia
		// is kept for posterity / debugging.
	}); err != nil {
		e.log.Warn("engine.session_update_err", "err", err)
	}
}

// RestoreSession re-adds every torrent recorded in the on-disk
// session manifest. Called by daemon.New right after engine.New
// returns so the GUI/web UI sees the user's last torrent list
// the moment they reopen the app. Order is preserved by saving
// and restoring queue_order; missing/corrupt .torrent file copies
// are retried via the saved magnet URI when available.
//
// Errors per-entry are logged at warn level and skipped — a single
// bad row in the manifest must not block restoring the rest.
func (e *Engine) RestoreSession() error {
	if e.sess == nil {
		return nil
	}
	entries := e.sess.list()
	if len(entries) == 0 {
		return nil
	}
	for _, entry := range entries {
		if err := e.restoreEntry(entry); err != nil {
			e.log.Warn("engine.session_restore_failed",
				"info_hash", entry.InfoHash,
				"added_via", entry.AddedVia,
				"err", err,
			)
			continue
		}
		e.log.Info("engine.session_restored",
			"info_hash", entry.InfoHash,
			"via", entry.AddedVia,
			"paused", entry.Paused,
			"indexing", entry.Indexing,
		)
	}
	return nil
}

// restoreEntry re-adds a single session entry to the engine,
// preferring the on-disk .torrent copy when present, falling back
// to the magnet URI, then the bare infohash.
func (e *Engine) restoreEntry(entry sessionEntry) error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return errors.New("engine: closed")
	}
	var (
		t   *torrent.Torrent
		err error
	)
	switch {
	case entry.TorrentFile != "" && e.sess.torrentsDir != "":
		path := filepath.Join(e.sess.torrentsDir, entry.TorrentFile)
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			err = rerr
			break
		}
		mi, lerr := metainfo.Load(bytes.NewReader(raw))
		if lerr != nil {
			err = lerr
			break
		}
		t, err = e.client.AddTorrent(mi)
	case entry.MagnetURI != "":
		t, err = e.client.AddMagnet(entry.MagnetURI)
	default:
		var ih metainfo.Hash
		if perr := ih.FromHexString(entry.InfoHash); perr != nil {
			err = perr
			break
		}
		t, _ = e.client.AddTorrentInfoHash(ih)
	}
	if err != nil {
		e.mu.Unlock()
		return err
	}
	if t == nil {
		e.mu.Unlock()
		return errors.New("engine: nil torrent after add")
	}
	h := e.registerLocked(t)
	if entry.SignedBy != "" {
		h.signedBy = entry.SignedBy
	}
	if entry.QueueOrder > e.nextQueueOrder {
		e.nextQueueOrder = entry.QueueOrder
	}
	e.mu.Unlock()

	// Apply paused / indexing state without re-persisting (the
	// session entry is already on disk in the desired shape).
	h.indexMu.Lock()
	h.indexing = entry.Indexing
	h.indexMu.Unlock()
	if entry.Paused {
		h.pausedMu.Lock()
		h.paused = true
		h.pausedMu.Unlock()
		h.T.DisallowDataDownload()
		h.T.DisallowDataUpload()
	}
	h.queueMu.Lock()
	h.queueOrder = entry.QueueOrder
	h.queueMu.Unlock()
	return nil
}

// autoDownload waits for a torrent's metadata to arrive and then
// sets every file's priority to Normal, matching the default
// behaviour of every mainstream BitTorrent client.
//
// We set priority per-file rather than calling Torrent.DownloadAll
// because anacrolix maintains two parallel priority surfaces:
// per-piece priorities (what the request strategy actually uses)
// and per-file priorities (exposed via File.Priority for display).
// DownloadAll flips only the former, leaving File.Priority stuck
// at "none" in the snapshot the GUI polls. Setting priority per
// file flips both, so the Files dialog shows "normal" and users
// who want to opt specific files out can flip individual entries
// back to "none" via Engine.SetFilePriority.
//
// The CLI used to call DownloadAll manually after printing file
// info; that path now relies on this goroutine instead. Calling
// DownloadAll additionally is idempotent, so the CLI's explicit
// call remains harmless.
func (e *Engine) autoDownload(h *Handle) {
	select {
	case <-h.T.GotInfo():
	case <-time.After(5 * time.Minute):
		return
	}
	e.mu.Lock()
	closed := e.closed
	e.mu.Unlock()
	if closed {
		return
	}
	// queueOrActivate decides: activate immediately under the
	// cap, otherwise mark handle as queued. Respects
	// MaxActiveDownloads.
	e.queueOrActivate(h)
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
	// Completion frees a download slot — promote the next queued
	// torrent if any.
	e.promoteQueuedLocked()
}

// ingestFileEvents drains a Handle's FileEvents() channel and submits each
// completed file to the content-ingestion pipeline. Runs in a background
// goroutine so the tracker is never blocked waiting for extraction.
//
// Each FileInput closes over the Handle + FileIndex so the pipeline can
// lazily open a reader via t.Files()[i].NewReader() only when the
// extractor actually wants to read the bytes.
func (e *Engine) ingestFileEvents(h *Handle) {
	events := h.SubscribeFileEvents()
	for ev := range events {
		e.mu.Lock()
		p := e.pipeline
		closed := e.closed
		e.mu.Unlock()
		if p == nil || closed {
			continue
		}
		// Per-torrent opt-out: the user can flip this flag at any
		// time via Engine.SetTorrentIndexing. A newly-flipped-off
		// torrent stops feeding its remaining files into the
		// pipeline; already-submitted chunks aren't recalled.
		if !h.IsIndexing() {
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
	doc.SignedBy = h.SignedBy()

	// Per-torrent opt-out: torrents explicitly marked non-indexing
	// skip the Bleve write but still go to the DHT publisher, so
	// the user can publish a torrent's existence without exposing
	// its file list in their own local searches.
	if idx != nil && h.IsIndexing() {
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

	// Auto-confirm torrents signed by a trusted publisher. A
	// trusted publisher's signature is the strongest "this
	// torrent is legitimate" signal we have: the user has
	// explicitly added the pubkey to their trust list, so we
	// don't need to wait for completion to mark it known-good.
	if h.SignedBy() != "" && e.trust != nil && e.trust.IsTrusted(h.SignedBy()) {
		if bf := e.KnownGoodBloom(); bf != nil {
			ih := h.T.InfoHash()
			bf.Add(ih[:])
			e.log.Info("engine.bloom.trusted_publisher_confirmed",
				"info_hash", ih.HexString(),
				"pubkey", h.SignedBy(),
				"label", e.trust.Label(h.SignedBy()),
			)
		}
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
	// Indexing reports whether this torrent feeds the local Bleve
	// index. Controlled via Engine.SetTorrentIndexing; default
	// true. Independent of whether an index is globally attached —
	// if Engine.SetIndex(nil), Indexing may still be true but
	// will have no effect.
	Indexing bool
	// IndexedFiles is the count of files the extraction pipeline
	// has finished handling for this torrent — includes extracted,
	// skipped (no matching extractor), and failed files. Advances
	// from 0 toward Files as the pipeline chews through completed
	// files; powers the GUI's per-torrent indexing progress bar.
	IndexedFiles int64
	// IndexExtracted is the subset of IndexedFiles that produced
	// at least one content chunk. Useful for gauging how much of
	// a large multimedia-heavy torrent is actually text.
	IndexExtracted int64
	// Queued is true when this torrent is waiting for a download
	// slot under the Engine.MaxActiveDownloads cap. Queued
	// torrents keep their metadata+indexing but do not download
	// file contents until a slot opens.
	Queued bool
	// DownloadRate is the average bytes/second received from
	// peers since the previous TorrentSnapshots call. Zero on
	// the first call (no prior sample) and while the torrent
	// has no metadata yet. Computed from anacrolix's
	// BytesReadUsefulData counter.
	DownloadRate int64
	// UploadRate mirrors DownloadRate for bytes sent to peers.
	UploadRate int64
	// SignedBy is the 64-char hex ed25519 public key that
	// signed this torrent's .torrent file. Empty when the
	// torrent is unsigned, verification failed, or the
	// torrent was added via magnet / raw infohash. See the
	// `internal/signing` package for the signing scheme.
	SignedBy string
	// TrustedPublisher is true when SignedBy is non-empty AND
	// the pubkey is in the Engine's trust store. Lets the GUI
	// render trusted torrents with a distinct badge.
	TrustedPublisher bool
}

// TorrentSnapshots returns a TorrentSnapshot for every torrent
// currently in the engine. Cheap enough to call from a polling
// HTTP handler — each snapshot is a few field reads plus one
// anacrolix Stats() call.
func (e *Engine) TorrentSnapshots() []TorrentSnapshot {
	handles := e.Torrents()
	e.mu.Lock()
	pipe := e.pipeline
	e.mu.Unlock()
	out := make([]TorrentSnapshot, 0, len(handles))
	for _, h := range handles {
		s := snapshotOf(h)
		// Populate TrustedPublisher from the engine's trust
		// store (snapshotOf itself has no engine reference, so
		// this second-pass fill-in keeps the helper pure).
		if s.SignedBy != "" && e.trust != nil && e.trust.IsTrusted(s.SignedBy) {
			s.TrustedPublisher = true
		}
		if pipe != nil {
			ps := pipe.Stats(s.InfoHash)
			s.IndexedFiles = ps.Processed
			s.IndexExtracted = ps.Extracted
		}
		out = append(out, s)
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

	queued := h.IsQueued()

	if t.Info() == nil {
		status := "metadata"
		if paused {
			status = "paused"
		} else if queued {
			status = "queued"
		}
		return TorrentSnapshot{
			InfoHash: ih,
			Name:     t.Name(),
			Paused:   paused,
			Status:   status,
			Indexing: h.IsIndexing(),
			Queued:   queued,
			SignedBy: h.SignedBy(),
		}
	}

	stats := t.Stats()
	size := t.Length()
	completed := t.BytesCompleted()
	missing := t.BytesMissing()
	files := len(t.Files())
	downRate, upRate := h.sampleRate()

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
	case queued:
		status = "queued"
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
		Indexing:       h.IsIndexing(),
		Queued:         queued,
		DownloadRate:   downRate,
		UploadRate:     upRate,
		SignedBy:       h.SignedBy(),
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
	e.persistState(h)
	// Pause frees a download slot — promote the next queued
	// torrent, if any.
	go e.promoteQueuedLocked()
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
	e.persistState(h)
	return nil
}

// SetTorrentIndexing flips the per-torrent indexing toggle for
// the given infohash. When set to false, future file completions
// for this torrent skip the content-extraction pipeline and the
// torrent-level document is not written to the Bleve index.
// Safe to call at any point in the torrent's lifecycle; the
// effect is prospective only — already-indexed chunks remain in
// the index. Idempotent.
//
// When disabling indexing for a torrent whose content is already
// in the Bleve index, call indexer.DeleteContentForTorrent /
// indexer.DeleteTorrent separately if you want the existing
// entries removed as well.
func (e *Engine) SetTorrentIndexing(infoHashHex string, enabled bool) error {
	h, err := e.handleByHex(infoHashHex)
	if err != nil {
		return err
	}
	h.indexMu.Lock()
	h.indexing = enabled
	h.indexMu.Unlock()
	e.log.Info("engine.torrent_indexing_set", "info_hash", infoHashHex, "enabled", enabled)
	e.persistState(h)
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
	if e.sess != nil {
		_ = e.sess.remove(h.T.InfoHash().HexString())
	}
	e.log.Info("engine.torrent_removed", "info_hash", infoHashHex)
	// Removing an active torrent frees a download slot — promote
	// the next queued torrent if any.
	go e.promoteQueuedLocked()
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
	// Cancel engine-owned background goroutines (VerifyData rehash
	// etc.) so they don't race with client shutdown below.
	if e.bgCancel != nil {
		e.bgCancel()
	}
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

// DHTAddr returns the local UDP address of the engine's DHT
// server (or nil if DHT is disabled). Exposed for wire-compat
// tests — external KRPC clients need the address to validate
// that the engine still responds to standard BEP-5 queries.
// The returned net.Addr is safe to stringify; tests typically
// resolve it back to *net.UDPAddr via net.ResolveUDPAddr.
func (e *Engine) DHTAddr() net.Addr {
	srv := e.dhtServer()
	if srv == nil {
		return nil
	}
	return srv.Addr()
}

// HandleByInfoHash looks up a *Handle by 20-byte infohash.
// Returns an error if the infohash is not currently registered
// with the engine. Intended for test use — the internal/testlab
// harness calls this to reach an anacrolix *Torrent for
// operations the wrapper doesn't expose directly (e.g.
// VerifyData, Stats, peer-list inspection).
func (e *Engine) HandleByInfoHash(ih [20]byte) (*Handle, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	h, ok := e.handles[metainfo.Hash(ih)]
	if !ok {
		return nil, fmt.Errorf("engine: no handle for infohash %x", ih[:8])
	}
	return h, nil
}

// AddTrustedPeerEngine wires every listen address of other into
// this engine's peer set for the given infohash, so the two
// engines can connect without a running DHT or tracker. The
// caller must have already added the same torrent (same
// infohash) to both engines via AddTorrentMetaInfo / AddInfoHash.
//
// Used by the internal/testlab package to build in-process
// multi-node clusters. Production code should not call this —
// peers discover each other through the DHT / trackers / PEX in
// the normal path.
//
// Returns the number of peer addresses that were added, or an
// error if this engine has no handle for the given infohash.
func (e *Engine) AddTrustedPeerEngine(ih [20]byte, other *Engine) (int, error) {
	if other == nil {
		return 0, errors.New("engine: nil other engine")
	}
	e.mu.Lock()
	h, ok := e.handles[metainfo.Hash(ih)]
	e.mu.Unlock()
	if !ok {
		return 0, fmt.Errorf("engine: no handle for infohash %x", ih[:8])
	}
	return h.T.AddClientPeer(other.client), nil
}
