// Package daemon is the single wiring point for a SwartzNet node. Every
// frontend — CLI, embedded web UI, native GUI — obtains a fully-wired node
// from New and differs only in presentation; subsystem lifecycle lives here
// and nowhere else.
package daemon

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/swartznet/swartznet/internal/admission"
	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
	"github.com/swartznet/swartznet/internal/httpapi"
	"github.com/swartznet/swartznet/internal/identity"
	"github.com/swartznet/swartznet/internal/indexer"
	"github.com/swartznet/swartznet/internal/searchmux"
)

// Options configures New. It grows slice by slice.
type Options struct {
	Cfg config.Config
	Log *slog.Logger // nil ⇒ slog.Default()

	// NoIndex prevents the Bleve index from ever opening. Mirrored into
	// Cfg.NoIndex BEFORE engine construction (SPEC §5.8 — the cascade also
	// disables Layer-D publishing when those slices land).
	NoIndex bool

	// APIAddr is the HTTP API listen address; "" disables the API entirely
	// (empty-path = feature-off).
	APIAddr string
	// Version is surfaced by GET /healthz.
	Version string
	// Stderr receives degraded-start warnings; nil ⇒ io.Discard.
	Stderr io.Writer
}

func (o Options) stderr() io.Writer {
	if o.Stderr == nil {
		return io.Discard
	}
	return o.Stderr
}

// Daemon is a fully-wired SwartzNet node. Exported subsystem handles are nil
// when the subsystem is disabled or failed a degraded (non-fatal) start.
type Daemon struct {
	Cfg config.Config
	Log *slog.Logger
	API *httpapi.Server // nil when APIAddr was empty or the bind failed
	// Identity is the loaded node identity; nil when IdentityPath is empty
	// or the load failed (degraded start — the node runs publisher-less).
	Identity *identity.Identity
	// Eng is the BitTorrent engine. Engine construction failure aborts New.
	Eng *engine.Engine
	// Idx is the Layer-L index; nil when NoIndex or IndexDir is empty.
	// Indexer open failure aborts New (the second fatal subsystem).
	Idx *indexer.Index
	// admission is the deny-by-default publisher-admission engine backing
	// the /aggregate counts. Its live feeder channels arrive with the
	// Aggregate slices; here it is correctly empty.
	admission *admission.AdmissionEngine

	bgCtx    context.Context
	bgCancel context.CancelFunc
	bgWG     sync.WaitGroup
	bgMu     sync.Mutex
	bgClosed bool

	closeOnce sync.Once
	closeErr  error
}

// New constructs and starts a node.
//
// Startup order (fixed; later slices insert without reordering): engine →
// indexer → companion publisher → companion subscriber → bootstrap → session
// restore → HTTP API last. Only engine and indexer failures abort New;
// everything else warns on Options.Stderr and starts degraded.
func New(ctx context.Context, opts Options) (*Daemon, error) {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	// Mirror NoIndex into the config BEFORE engine construction: the engine
	// (and later the indexer + Layer-D publisher) read the config copy.
	if opts.NoIndex {
		opts.Cfg.NoIndex = true
	}
	if err := opts.Cfg.Validate(); err != nil {
		return nil, err
	}

	bgCtx, bgCancel := context.WithCancel(ctx)
	d := &Daemon{
		Cfg:      opts.Cfg,
		Log:      log,
		bgCtx:    bgCtx,
		bgCancel: bgCancel,
	}
	log.Debug("daemon.new", "data_dir", opts.Cfg.DataDir, "index_dir", opts.Cfg.IndexDir, "api_addr", opts.APIAddr)

	// Identity precedes every subsystem: the engine and publishers consume
	// its Signer. Auto-create is allowed only when the configured path IS the
	// default XDG path — Load's create branch enforces it. Failure degrades
	// (SPEC §2.8: the node still downloads and searches, it cannot publish).
	if opts.Cfg.IdentityPath != "" {
		// Clean the configured side: Default()'s value is already Join-cleaned,
		// and a `/./`- or `//`-spelled default path must still count as default.
		allowCreate := filepath.Clean(opts.Cfg.IdentityPath) == config.Default().IdentityPath
		id, err := identity.Load(opts.Cfg.IdentityPath, allowCreate)
		if err != nil {
			log.Warn("daemon.identity_load_err", "err", err)
			fmt.Fprintf(opts.stderr(), "warning: identity load failed: %v\n", err)
		} else {
			d.Identity = id
			log.Info("daemon.identity_loaded", "pubkey", id.PublicKeyHex())
		}
	}

	// Engine construction is one of the two fatal startup steps (the other
	// is the indexer, next slice).
	eng, err := engine.New(ctx, opts.Cfg, log)
	if err != nil {
		bgCancel()
		return nil, err
	}
	d.Eng = eng

	// Layer L: open the index and attach it to the engine, unless disabled.
	// Open failure aborts New (the second fatal subsystem); NoIndex or an
	// empty IndexDir skips cleanly (nil index, degraded API).
	if !opts.Cfg.NoIndex && opts.Cfg.IndexDir != "" {
		idx, err := indexer.OpenWithLogger(opts.Cfg.IndexDir, log)
		if err != nil {
			bgCancel()
			_ = eng.Close()
			return nil, fmt.Errorf("open index: %w", err)
		}
		d.Idx = idx
		eng.SetIndex(idx)
	}

	// The deny-by-default admission engine. Its reputation view adapts the
	// engine's tracker (nil-safe: an unknown pubkey scores the neutral
	// prior). Empty anchors ship by design — a curated seeds.json is a
	// release prerequisite, not code (B8/B9). Construction failure is
	// non-fatal (the engine only backs /aggregate counts).
	if adm, err := admission.NewEngine(admission.DefaultPolicy(), reputationView{eng: eng}, admission.DefaultAnchorPubkeys, log); err != nil {
		log.Warn("daemon.admission_init_err", "err", err)
	} else {
		d.admission = adm
	}

	// (companion, bootstrap land here, in that order.)

	// The signer mints Aggregate records on GotInfo; wire it BEFORE restore so
	// restored torrents mint too. The daemon owns identity (not engine.New).
	if d.Identity != nil {
		var pub [32]byte
		copy(pub[:], d.Identity.PublicKey)
		eng.SetSigner(d.Identity.PrivateKey, pub)
	}

	// Session restore runs before the HTTP API so restored torrents are
	// visible to the first request (and their autoIndex finds the index).
	// Per-entry failures only warn.
	_ = eng.RestoreSession()

	if opts.APIAddr != "" {
		apiOpts := httpapi.Options{Version: opts.Version}
		if d.Identity != nil {
			apiOpts.PublisherPubKey = d.Identity.PublicKeyHex
		}
		adapter := &controllerAdapter{eng: eng, adm: d.admission}
		// Always wire /search so Layer S (swarm) works even with no local
		// index; Local stays nil (200-empty) when the index is off.
		mux := &searchmux.Mux{Swarm: &swarmSearchAdapter{eng: eng}}
		if idx := eng.Index(); idx != nil {
			mux.Local = idx
		}
		apiOpts.Search = adapter.search(mux)
		if d.Idx != nil {
			apiOpts.IndexStats = adapter.indexStats
			apiOpts.LocalDocCount = adapter.localDocCount
		}
		apiOpts.Adder = adapter
		apiOpts.Control = adapter
		apiOpts.Confirm = d.Confirm
		apiOpts.Flag = d.Flag
		apiOpts.BloomStat = adapter.bloomStat
		apiOpts.ReputationStat = adapter.reputationStat
		apiOpts.Aggregate = adapter.aggregate
		apiOpts.ServicesReporter = eng.ServicesMask
		apiOpts.Capabilities = adapter
		apiOpts.SwarmStatus = func() (int, int) {
			sw := eng.SwarmSearch()
			return sw.KnownPeers(), sw.CapablePeerCount()
		}
		if !opts.Cfg.DisableDHT {
			apiOpts.DHTStats = eng.DHTRoutingTableSize
		}
		api := httpapi.NewWithOptions(opts.APIAddr, log, apiOpts)
		if err := api.Start(); err != nil {
			fmt.Fprintf(opts.stderr(), "warning: httpapi start failed: %v\n", err)
		} else {
			d.API = api
		}
	}

	return d, nil
}

// goBG runs fn on the daemon-owned background context. Close cancels that
// context and joins every such goroutine before any subsystem teardown, so a
// background job can never touch a subsystem mid-teardown. Registration and
// teardown share a lock: once Close has begun, goBG is a no-op — otherwise an
// Add racing Wait would spawn a goroutine the join can never see.
func (d *Daemon) goBG(fn func(context.Context)) {
	d.bgMu.Lock()
	if d.bgClosed {
		d.bgMu.Unlock()
		return
	}
	d.bgWG.Add(1)
	d.bgMu.Unlock()
	go func() {
		defer d.bgWG.Done()
		fn(d.bgCtx)
	}()
}

// Close tears the node down in strict reverse startup order. It is
// idempotent: repeated calls return the first call's error.
func (d *Daemon) Close() error {
	d.closeOnce.Do(func() {
		d.Log.Info("daemon.close_begin")
		// Background context first: nothing may touch a subsystem mid-teardown.
		d.bgMu.Lock()
		d.bgClosed = true
		d.bgMu.Unlock()
		d.bgCancel()
		d.bgWG.Wait()
		d.Log.Info("daemon.bg_joined")
		if d.API != nil {
			shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = d.API.Stop(shutdown)
			d.Log.Info("httpapi.stopped")
		}
		// (companion subscriber → companion publisher → indexer teardown
		// lands here, each with its own log line.)
		if d.Eng != nil {
			if err := d.Eng.Close(); err != nil {
				d.closeErr = err
			}
			d.Log.Info("engine.stopped")
		}
		// Engine.Close stopped the pipeline; now close the index the daemon
		// opened.
		if d.Idx != nil {
			if err := d.Idx.Close(); err != nil {
				d.closeErr = err
			}
			d.Log.Info("indexer.stopped")
		}
		d.Log.Info("daemon.close_done")
	})
	return d.closeErr
}
