// Package engine wraps the anacrolix BitTorrent client. It is the single
// owner of the client and its DHT server, integrated ONLY through extension
// APIs (config closures, callbacks, storage options) — the vendored library
// is never patched (MPL discipline). The many anacrolix quirks named in
// SPEC §5.4 are honored inside this package and nowhere else.
package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/anacrolix/dht/v2"
	peer_store "github.com/anacrolix/dht/v2/peer-store"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	pp "github.com/anacrolix/torrent/peer_protocol"
	"golang.org/x/time/rate"

	"github.com/swartznet/swartznet/contracts/ltepwire"
	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/indexer"
	"github.com/swartznet/swartznet/internal/reputation"
	"github.com/swartznet/swartznet/internal/swarmsearch"
	"github.com/swartznet/swartznet/internal/trust"
)

// unlimitedBurst keeps the rate limiters' burst positive even in unlimited
// mode: anacrolix's openNewConns refuses to dial when the download limiter
// has no tokens, so a zero burst silently blocks every outgoing connection.
const unlimitedBurst = math.MaxInt32

// Engine is the BitTorrent engine: one anacrolix client, its DHT server,
// the download queue, session persistence, and per-torrent bookkeeping.
type Engine struct {
	cfg    config.Config
	log    *slog.Logger
	client *torrent.Client

	ulLimiter *rate.Limiter
	dlLimiter *rate.Limiter

	// bgCtx is deliberately rooted in context.Background(): Close is its
	// only canceller, so teardown is deterministic regardless of the
	// caller-context's lifecycle.
	bgCtx    context.Context
	bgCancel context.CancelFunc

	mu             sync.Mutex
	handles        map[metainfo.Hash]*Handle
	nextQueueOrder int64
	closed         bool
	closeDone      chan struct{} // closed when the first Close finishes
	closeErr       error

	// promoteMu serializes count-then-activate so a batch restore's
	// concurrent autoDownload goroutines cannot over-subscribe the cap.
	promoteMu          sync.Mutex
	maxActiveDownloads int // 0 = unlimited; runtime-only, resets on restart

	sess *session

	// idxMu guards the Layer-L index + pipeline attachment (SetIndex may
	// race the per-torrent index goroutines).
	idxMu    sync.Mutex
	idx      *indexer.Index
	pipeline *indexer.Pipeline

	// rescanInterval is this engine's hourly-rescan cadence, an instance
	// field (not a shared global) so tests can shrink it without racing
	// other engines' rescan goroutines. Each tick re-submits completed
	// files whose file-complete event was dropped by the fan-out
	// (SPEC §5.5); deterministic doc IDs make the re-submit idempotent.
	rescanInterval time.Duration

	// Spam-resistance subsystems (nil = feature off). repMu guards the
	// bloom+tracker attachments against the checkpoint goroutine.
	repMu   sync.Mutex
	bloom   *reputation.BloomFilter
	tracker *reputation.Tracker
	sources *reputation.SourceTracker // always non-nil
	trust   *trust.Store
	// trustFailed is true when TrustPath was configured but the store
	// could not be loaded (corrupt/unreadable file). Distinguished from
	// "trust off" (nil trust, trustFailed false) so FlagHit can fail
	// CLOSED — it must never demote when it cannot check the exemption.
	trustFailed bool

	// checkpointInterval is this engine's Bloom+reputation flush cadence
	// (instance field, like rescanInterval — no shared global to race).
	checkpointInterval time.Duration
	// ckptMu serializes Checkpoint so the periodic ticker, confirm/flag,
	// completion, and Close never overlap their bloom+tracker saves.
	ckptMu sync.Mutex
	// ckptWG joins the checkpoint goroutine so Close's final flush is the
	// authoritative last write (no late ticker save clobbers it).
	ckptWG sync.WaitGroup

	// sharing is the operator's runtime sn_search sharing prefs (Slice 6),
	// seeded from cfg at New and mutated via PATCH /capabilities. Guarded by
	// shareMu; runtime-only (not persisted), like the rate limits.
	shareMu sync.Mutex
	sharing ltepwire.Sharing

	// swarm is the Layer-S sn_search peer-wire protocol (Slice 7); swarmPeers
	// tracks addr→conn for the token-gated sender.
	swarm      *swarmsearch.Protocol
	swarmPeers *peerTracker
}

// defaultRescanInterval is the production hourly cadence.
const defaultRescanInterval = time.Hour

// defaultCheckpointInterval bounds crash loss of Bloom/reputation to one
// interval (D23 — the legacy saved only at clean Close).
const defaultCheckpointInterval = 5 * time.Minute

// New constructs the engine. cfg must already be Validate()d by the caller
// (the daemon); Validate is re-run here as a safety net since it is
// idempotent. Session-load failures never fail New — the node starts with an
// empty session and warns.
func New(ctx context.Context, cfg config.Config, log *slog.Logger) (*Engine, error) {
	_ = ctx // reserved for the Layer-S feeler; bgCtx is engine-owned
	if log == nil {
		log = slog.Default()
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	tc := torrent.NewDefaultClientConfig()
	tc.DataDir = cfg.DataDir
	tc.ListenPort = cfg.ListenPort
	if cfg.ListenHost != "" {
		host := cfg.ListenHost
		tc.ListenHost = func(string) string { return host }
	}
	tc.Seed = cfg.Seed
	tc.NoUpload = cfg.NoUpload
	tc.NoDHT = cfg.DisableDHT
	tc.DisableIPv6 = cfg.DisableIPv6
	tc.NoDefaultPortForwarding = cfg.DisablePortForwarding
	if cfg.HTTPUserAgent != "" {
		tc.HTTPUserAgent = cfg.HTTPUserAgent
	}

	// One shared PeerStore instance: without it the DHT server's get_peers
	// replies carry no BEP-5 write token and vanilla announce_peer stalls —
	// a mainline-compat break invisible in self-tests. The same instance
	// backs our own per-infohash peer table.
	peerStore := &peer_store.InMemory{}
	bootstrap := append([]string(nil), cfg.DHTBootstrapAddrs...)
	tc.ConfigureAnacrolixDhtServer = func(sc *dht.ServerConfig) {
		if sc.PeerStore == nil {
			sc.PeerStore = peerStore
		}
		if len(bootstrap) > 0 {
			sc.StartingNodes = func() ([]dht.Addr, error) {
				return dht.ResolveHostPorts(bootstrap)
			}
		}
		if cfg.DHTInsecure {
			sc.NoSecurity = true
		}
		// NewAnacrolixDhtServer leaves Exp zero, which makes every stored
		// BEP-44 item instantly expired ("put succeeds, get finds nothing").
		// Harmless for pure BEP-5; load-bearing for the Layer-D slice.
		if sc.Exp == 0 {
			sc.Exp = 2 * time.Hour
		}
	}

	ul := rate.NewLimiter(rate.Inf, unlimitedBurst)
	dl := rate.NewLimiter(rate.Inf, unlimitedBurst)
	tc.UploadRateLimiter = ul
	tc.DownloadRateLimiter = dl

	tc.Callbacks.StatusUpdated = append(tc.Callbacks.StatusUpdated, func(ev torrent.StatusUpdatedEvent) {
		log.Debug("torrent.status", "event", ev.Event, "info_hash", ev.InfoHash, "url", ev.Url, "err", ev.Error)
	})

	// --- Layer S: the sn_search LTEP transport seam (Slice 7) ---
	// Built before NewClient so the callbacks close over the protocol +
	// tracker + semaphores. Capabilities/searcher are injected after the
	// engine exists (they need e.Sharing/e.ServicesMask/e.Index).
	swarm := swarmsearch.New(log)
	peers := newPeerTracker()
	snSearchSem := make(chan struct{}, maxInboundSnSearchWorkers)
	snReplySem := make(chan struct{}, maxInboundSnSearchWorkers)
	swarm.SetTransport(&swarmSender{peers: peers})

	// PeerConnAdded: advertise sn_search in OUR outbound m dict + record the
	// conn. A vanilla peer just sees an ignorable name it does not list back.
	tc.Callbacks.PeerConnAdded = append(tc.Callbacks.PeerConnAdded, func(pc *torrent.PeerConn) {
		pc.LocalLtepProtocolMap.AddUserProtocol(extName)
		addr := pc.RemoteAddr.String()
		swarm.NotePeerAdded(addr)
		peers.add(addr, pc)
	})
	// ReadExtendedHandshake: record whether the remote advertised sn_search
	// (present with a non-zero id). This is the ONLY place a PeerToken is
	// minted — from the m dict, never from a peer_announce.
	tc.Callbacks.ReadExtendedHandshake = func(pc *torrent.PeerConn, hs *pp.ExtendedHandshakeMessage) {
		id := hs.M[extName]
		swarm.OnRemoteHandshake(pc.RemoteAddr.String(), id != 0, int(id))
	}
	// PeerConnReadExtensionMessage: dispatch inbound sn_search frames OFF the
	// read loop (which holds the client lock). Admit via the handler
	// semaphore, copy the payload (the decoder reuses its buffer), then hand
	// to the protocol on a goroutine with a gated reply writer.
	tc.Callbacks.PeerConnReadExtensionMessage = append(tc.Callbacks.PeerConnReadExtensionMessage,
		func(ev torrent.PeerConnReadExtensionMessageEvent) {
			name, _, err := ev.PeerConn.LocalLtepProtocolMap.LookupId(ev.ExtensionNumber)
			if err != nil || name != extName {
				return
			}
			select {
			case snSearchSem <- struct{}{}:
			default:
				log.Debug("engine.swarm.inbound_dropped_overloaded")
				return
			}
			payload := append([]byte(nil), ev.Payload...)
			pc := ev.PeerConn
			addr := pc.RemoteAddr.String()
			go func() {
				defer func() { <-snSearchSem }()
				swarm.HandleMessage(addr, payload, gatedReply(snReplySem, pc, log))
			}()
		})
	tc.Callbacks.PeerConnClosed = func(pc *torrent.PeerConn) {
		addr := pc.RemoteAddr.String()
		swarm.OnPeerClosed(addr)
		peers.remove(addr)
	}

	cl, err := torrent.NewClient(tc)
	if err != nil {
		swarm.Close()
		return nil, fmt.Errorf("engine: new client: %w", err)
	}

	bgCtx, bgCancel := context.WithCancel(context.Background())
	e := &Engine{
		cfg:                cfg,
		log:                log,
		client:             cl,
		ulLimiter:          ul,
		dlLimiter:          dl,
		bgCtx:              bgCtx,
		bgCancel:           bgCancel,
		handles:            make(map[metainfo.Hash]*Handle),
		closeDone:          make(chan struct{}),
		rescanInterval:     cfg.IndexRescanInterval,
		checkpointInterval: cfg.CheckpointInterval,
		sources:            reputation.NewSourceTracker(0),
		// cfg is Validate()d above, so ShareLocal ∈ 0..2 fits uint8.
		sharing: ltepwire.Sharing{
			ShareLocal:  uint8(cfg.ShareLocal),
			FileHits:    cfg.ShareFileHits,
			ContentHits: cfg.ShareContentHits,
		},
		swarm:      swarm,
		swarmPeers: peers,
	}
	// Feed the single mask producer to the outbound peer_announce + inbound
	// scope decisions (Slice 6's Announced, live).
	swarm.SetCapabilitySource(func() swarmsearch.Capabilities {
		return swarmsearch.Capabilities{Sharing: e.Sharing(), Services: e.ServicesMask()}
	})
	if e.rescanInterval <= 0 {
		e.rescanInterval = defaultRescanInterval
	}
	if e.checkpointInterval <= 0 {
		e.checkpointInterval = defaultCheckpointInterval
	}
	e.loadSpamResistance(cfg)

	log.Info("engine.started",
		"data_dir", cfg.DataDir,
		"listen_port", cl.LocalPort(),
		"peer_id", fmt.Sprintf("%x", cl.PeerID()),
		"dht_enabled", !cfg.DisableDHT)
	if cfg.Regtest {
		log.Warn("engine.regtest_mode_active", "warning", "DO NOT USE IN PRODUCTION")
	}

	sess, err := loadSession(cfg.DataDir)
	if sess == nil {
		// Only the torrents-dir mkdir fails this hard — persistence would be
		// impossible for the whole run, which is fatal (legacy shape).
		bgCancel()
		_ = cl.Close()
		return nil, err
	}
	if err != nil {
		// Corrupt manifest: warn and continue — the session keeps its paths,
		// so the next save rewrites a valid file (self-healing).
		log.Warn("engine.session_load_err", "err", err)
	}
	e.sess = sess
	if n := len(sess.list()); n > 0 {
		log.Info("engine.session_loaded", "path", sess.path, "entries", n)
	}

	go e.runIndexRescan()
	e.ckptWG.Add(1)
	go e.runReputationCheckpoint()

	return e, nil
}

// Close tears the engine down. Idempotent and concurrency-safe: late callers
// block until the first call's teardown finishes, then return its error.
func (e *Engine) Close() error {
	e.mu.Lock()
	if e.closed {
		done := e.closeDone
		e.mu.Unlock()
		<-done
		e.mu.Lock()
		err := e.closeErr
		e.mu.Unlock()
		return err
	}
	e.closed = true
	e.bgCancel()
	handles := make([]*Handle, 0, len(e.handles))
	for _, h := range e.handles {
		handles = append(handles, h)
	}
	e.mu.Unlock()

	// Stop the extraction pipeline before storage teardown. The index
	// itself is closed by the daemon (it owns the handle it opened).
	e.idxMu.Lock()
	if e.pipeline != nil {
		e.pipeline.Stop()
		e.pipeline = nil
	}
	e.idxMu.Unlock()
	// Stop the Layer-S announce worker.
	if e.swarm != nil {
		e.swarm.Close()
	}
	// Join the checkpoint goroutine (bgCancel above stops its ticker loop)
	// so no late periodic save can run after — and thus never clobber — the
	// final flush below.
	e.ckptWG.Wait()
	// Final Bloom+reputation flush captures the last sub-checkpoint interval
	// (the periodic checkpoint bounds crash loss; this bounds clean-close
	// loss to zero).
	e.Checkpoint()
	for _, h := range handles {
		h.pieceSub.Close()
		h.fileSub.Close()
	}
	var closeErr error
	if errs := e.client.Close(); len(errs) > 0 {
		closeErr = errors.Join(errs...)
	}
	e.mu.Lock()
	e.closeErr = closeErr
	e.mu.Unlock()
	close(e.closeDone)
	e.log.Info("engine.closed", "err", closeErr)
	return closeErr
}

// Torrents returns a fresh slice of the current handles.
func (e *Engine) Torrents() []*Handle {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]*Handle, 0, len(e.handles))
	for _, h := range e.handles {
		out = append(out, h)
	}
	return out
}

// LocalPort reports the resolved BitTorrent listen port.
func (e *Engine) LocalPort() int { return e.client.LocalPort() }

// dhtServer unwraps the first anacrolix DHT server, nil when DHT is off.
func (e *Engine) dhtServer() *dht.Server {
	for _, s := range e.client.DhtServers() {
		if w, ok := s.(torrent.AnacrolixDhtServerWrapper); ok {
			return w.Server
		}
	}
	return nil
}

// DHTRoutingTableSize reports (good, total) routing-table nodes; (0,0) when
// the DHT is disabled.
func (e *Engine) DHTRoutingTableSize() (good, total int) {
	srv := e.dhtServer()
	if srv == nil {
		return 0, 0
	}
	stats := srv.Stats()
	return stats.GoodNodes, stats.Nodes
}

// handleByHex resolves a 40-hex infohash to a handle.
func (e *Engine) handleByHex(ihHex string) (*Handle, error) {
	var hash metainfo.Hash
	if err := hash.FromHexString(ihHex); err != nil {
		return nil, fmt.Errorf("engine: invalid infohash %q: %w", ihHex, err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	h, ok := e.handles[hash]
	if !ok {
		return nil, fmt.Errorf("engine: no torrent with infohash %s", ihHex)
	}
	return h, nil
}

// HandleByInfoHash resolves a raw 20-byte infohash.
func (e *Engine) HandleByInfoHash(ih [20]byte) (*Handle, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	h, ok := e.handles[metainfo.Hash(ih)]
	if !ok {
		return nil, fmt.Errorf("engine: no handle for infohash %x", ih[:8])
	}
	return h, nil
}

// AddTrustedPeerEngine cross-wires two in-process engines as peers for the
// given torrent — the deterministic no-DHT harness hook. Returns the number
// of peer addresses actually added (post-dedupe).
func (e *Engine) AddTrustedPeerEngine(ih [20]byte, other *Engine) (int, error) {
	if other == nil {
		return 0, fmt.Errorf("engine: nil other engine")
	}
	h, err := e.HandleByInfoHash(ih)
	if err != nil {
		return 0, err
	}
	return h.T.AddClientPeer(other.client), nil
}

// PauseTorrent pauses a torrent (soft: peer connections stay open).
// Idempotent.
func (e *Engine) PauseTorrent(ihHex string) error {
	h, err := e.handleByHex(ihHex)
	if err != nil {
		return err
	}
	if !h.setPaused(true) {
		return nil
	}
	h.T.DisallowDataDownload()
	h.T.DisallowDataUpload()
	e.log.Info("engine.torrent_paused", "info_hash", h.InfoHashHex())
	e.persistState(h)
	go e.promoteQueued()
	return nil
}

// ResumeTorrent resumes a paused torrent. Idempotent.
func (e *Engine) ResumeTorrent(ihHex string) error {
	h, err := e.handleByHex(ihHex)
	if err != nil {
		return err
	}
	if !h.setPaused(false) {
		return nil
	}
	h.T.AllowDataDownload()
	h.T.AllowDataUpload()
	e.log.Info("engine.torrent_resumed", "info_hash", h.InfoHashHex())
	e.persistState(h)
	go e.queueOrActivate(h)
	return nil
}

// RemoveTorrent drops a torrent and forgets its session entry. Downloaded
// data stays on disk.
func (e *Engine) RemoveTorrent(ihHex string) error {
	h, err := e.handleByHex(ihHex)
	if err != nil {
		return err
	}
	h.markRemoved()
	h.pieceSub.Close()
	h.fileSub.Close()
	h.T.Drop()
	e.mu.Lock()
	delete(e.handles, h.T.InfoHash())
	e.mu.Unlock()
	if _, pipeline := e.index(); pipeline != nil {
		pipeline.ForgetSubmitted(h.InfoHashHex())
	}
	e.sess.remove(h.InfoHashHex())
	e.log.Info("engine.torrent_removed", "info_hash", h.InfoHashHex())
	go e.promoteQueued()
	return nil
}
