package daemon

import (
	"context"
	"log/slog"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
	"github.com/swartznet/swartznet/internal/rendezvous"
)

// rendezvousReconcileInterval re-joins any rendezvous swarm that was dropped.
// The desired set is static per process, so this is pure defense — the first
// reconcile does all the real work.
const rendezvousReconcileInterval = 5 * time.Minute

// engineRendezvous adapts *engine.Engine to rendezvous.Joiner. The engine's
// AddRendezvous returns a handle the manager doesn't need; this drops it.
type engineRendezvous struct{ eng *engine.Engine }

func (a engineRendezvous) AddRendezvous(h metainfo.Hash, community bool) error {
	_, err := a.eng.AddRendezvous(h, community)
	return err
}
func (a engineRendezvous) RemoveRendezvous(h metainfo.Hash) error { return a.eng.RemoveRendezvous(h) }
func (a engineRendezvous) RendezvousInfoHashes() []string         { return a.eng.RendezvousInfoHashes() }

// startRendezvous launches the rendezvous manager on the daemon background
// context (joined on Close). It is a no-op when nothing is configured to join,
// or when the DHT is off (a rendezvous swarm can't find peers without it).
func startRendezvous(d *Daemon, eng *engine.Engine, cfg config.Config, log *slog.Logger) {
	rvCfg := rendezvous.Config{
		Global:      cfg.RendezvousGlobal,
		Topics:      cfg.RendezvousTopics,
		Communities: cfg.RendezvousCommunities,
	}
	desired := rvCfg.DesiredSwarms()
	if len(desired) == 0 {
		return
	}
	if cfg.DisableDHT {
		log.Info("rendezvous.skipped_dht_off", "would_join", len(desired))
		return
	}
	mgr := rendezvous.NewManager(engineRendezvous{eng}, rvCfg, log)
	// Count only — never log the community secrets, and keep the topic strings
	// out of the log too (they reveal this node's interests).
	log.Info("rendezvous.started",
		"swarms", len(desired),
		"global", rvCfg.Global,
		"topics", len(rvCfg.Topics),
		"communities", len(rvCfg.Communities))
	d.goBG(func(ctx context.Context) { mgr.Run(ctx, rendezvousReconcileInterval) })
}
