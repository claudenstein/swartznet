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
	"sync"
	"time"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/httpapi"
)

// Options configures New. It grows slice by slice.
type Options struct {
	Cfg config.Config
	Log *slog.Logger // nil ⇒ slog.Default()

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

	// (engine, indexer, companion, bootstrap, session restore land here,
	// in that order, as their slices arrive.)

	if opts.APIAddr != "" {
		api := httpapi.NewWithOptions(opts.APIAddr, log, httpapi.Options{Version: opts.Version})
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
		// (companion subscriber → companion publisher → indexer → engine
		// teardown lands here, each with its own log line.)
		d.Log.Info("daemon.close_done")
	})
	return d.closeErr
}
