// Package daemon wires a fully-operational SwartzNet node from the
// individual internal packages. Both the CLI (cmd/swartznet) and the
// GUI (cmd/swartznet-gui) call daemon.New to get a ready-to-use
// Daemon; they differ only in how they present the results to the
// user.
package daemon

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/swartznet/swartznet/internal/companion"
	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
	"github.com/swartznet/swartznet/internal/httpapi"
	"github.com/swartznet/swartznet/internal/indexer"
)

// Daemon holds a fully-wired SwartzNet node. Construct with New;
// always call Close when done. Fields are exported so callers
// (CLI, GUI) can reach the subsystems directly.
type Daemon struct {
	Eng     *engine.Engine
	Index   *indexer.Index              // nil when NoIndex is set
	CompPub *companion.Publisher        // nil when conditions unmet
	CompSub *companion.SubscriberWorker // nil when conditions unmet
	API     *httpapi.Server             // nil when APIAddr is empty
	Cfg     config.Config
	Log     *slog.Logger
}

// Options controls which subsystems daemon.New starts.
type Options struct {
	Cfg     config.Config
	Log     *slog.Logger
	NoIndex bool   // skip Bleve index
	APIAddr string // HTTP API listen address; "" disables
	Version string // shown in /healthz
	// Stderr receives non-fatal warnings (e.g. companion setup
	// failures). Defaults to io.Discard when nil.
	Stderr io.Writer
}

func (o *Options) stderr() io.Writer {
	if o.Stderr != nil {
		return o.Stderr
	}
	return io.Discard
}

// New constructs and starts every subsystem of a SwartzNet node.
// The returned Daemon is ready to use; call Close to tear it down.
// The ctx governs the lifetime of the underlying torrent client.
func New(ctx context.Context, opts Options) (*Daemon, error) {
	d := &Daemon{
		Cfg: opts.Cfg,
		Log: opts.Log,
	}
	stderr := opts.stderr()

	// --- engine ---
	eng, err := engine.New(ctx, opts.Cfg, opts.Log)
	if err != nil {
		return nil, err
	}
	d.Eng = eng

	// --- indexer ---
	if !opts.NoIndex {
		idx, err := indexer.Open(opts.Cfg.IndexDir)
		if err != nil {
			_ = eng.Close()
			return nil, fmt.Errorf("open index: %w", err)
		}
		d.Index = idx
		eng.SetIndex(idx)
	}

	// --- companion publisher (M11c) ---
	if d.Index != nil && eng.PointerPutter() != nil && eng.Identity() != nil && opts.Cfg.CompanionDir != "" {
		cpOpts := companion.DefaultPublisherOptions()
		if opts.Cfg.Regtest {
			cpOpts = companion.RegtestPublisherOptions()
		}
		cpOpts.Dir = opts.Cfg.DataDir
		cpOpts.PublisherKey = eng.Identity().PublicKeyBytes()
		compPub, err := companion.NewPublisher(d.Index, eng.PointerPutter(), eng, cpOpts, opts.Log)
		if err != nil {
			fmt.Fprintf(stderr, "warning: companion publisher start failed: %v\n", err)
		} else {
			compPub.Start()
			d.CompPub = compPub
		}
	}

	// --- companion subscriber (M11d) ---
	if d.Index != nil && eng.PointerGetter() != nil && opts.Cfg.CompanionDir != "" {
		sub, err := companion.NewSubscriber(
			eng.PointerGetter(), eng, d.Index,
			companion.DefaultSubscriberOptions(),
			opts.Log,
		)
		if err != nil {
			fmt.Fprintf(stderr, "warning: companion subscriber init failed: %v\n", err)
		} else {
			compSub, err := companion.NewSubscriberWorker(sub)
			if err != nil {
				fmt.Fprintf(stderr, "warning: companion subscriber worker init failed: %v\n", err)
			} else {
				if opts.Cfg.CompanionFollowFile != "" {
					LoadFollowFile(compSub, opts.Cfg.CompanionFollowFile, stderr)
				}
				compSub.Start()
				d.CompSub = compSub
			}
		}
	}

	// --- Session restore ---
	// Re-add every torrent recorded in the on-disk session manifest so
	// the user sees their previous list when reopening the GUI/web UI.
	// Failures per-entry are logged at warn level inside the engine and
	// must not block daemon startup, so we ignore the returned error.
	_ = eng.RestoreSession()

	// --- HTTP API ---
	if opts.APIAddr != "" {
		httpapi.SetHealthzVersion(opts.Version)
		api := httpapi.NewWithOptions(opts.APIAddr, opts.Log, httpapi.Options{
			Index:     d.Index,
			Swarm:     eng.SwarmSearch(),
			Publisher: eng.Publisher(),
			Lookup:    eng.Lookup(),
			Bloom:     eng.KnownGoodBloom(),
			Tracker:   eng.ReputationTracker(),
			Sources:   eng.SourceTracker(),
			Adder:     eng,
			Control:   &controllerAdapter{eng: eng},
			Companion: newCompanionAdapter(d.CompPub, d.CompSub, opts.Cfg.CompanionFollowFile),
			DHTStats:  eng.DHTRoutingTableSize,
		})
		if err := api.Start(); err != nil {
			fmt.Fprintf(stderr, "warning: httpapi start failed: %v\n", err)
		} else {
			d.API = api
		}
	}

	return d, nil
}

// Close tears down every subsystem in reverse startup order.
func (d *Daemon) Close() error {
	if d.API != nil {
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = d.API.Stop(shutdown)
	}
	if d.CompSub != nil {
		d.CompSub.Stop()
	}
	if d.CompPub != nil {
		d.CompPub.Stop()
	}
	if d.Index != nil {
		d.Index.Close()
	}
	return d.Eng.Close()
}
