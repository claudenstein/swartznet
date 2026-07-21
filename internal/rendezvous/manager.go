package rendezvous

import (
	"context"
	"log/slog"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

// Joiner is the engine surface the Manager drives. *engine.Engine satisfies it
// through a thin adapter in the daemon (its AddRendezvous returns a handle the
// Manager doesn't need). Kept as an interface here so the Manager is unit-tested
// against a fake with no engine/anacrolix dependency.
type Joiner interface {
	// AddRendezvous joins a swarm. community marks a PRIVATE community swarm,
	// whose infohash is a secret meeting point — the engine must keep untrusted
	// (PEX-learned) peers out of it so the secret infohash is never dialed in a
	// context an outsider can observe.
	AddRendezvous(hash metainfo.Hash, community bool) error
	RemoveRendezvous(hash metainfo.Hash) error
	// RendezvousInfoHashes returns the hex infohashes currently joined.
	RendezvousInfoHashes() []string
}

// DesiredSwarm is one rendezvous swarm the config asks to join, tagged with
// whether it is a private community swarm.
type DesiredSwarm struct {
	Hash      metainfo.Hash
	Community bool
}

// Config is the operator's rendezvous preference: whether to join the public
// global swarm, plus any topic and private-community swarms. It is static for a
// process, so a single reconcile at startup suffices; the periodic reconcile
// exists only to re-join a swarm that was somehow dropped.
type Config struct {
	Global      bool     // join the public global swarm (default on)
	Topics      []string // per-topic swarms
	Communities []string // private-community shared secrets (SENSITIVE — never log)
}

// DesiredSwarms returns the deduped set of rendezvous swarms this config asks to
// join, in a stable order (global, then topics, then communities, each in input
// order). Empty/invalid topics and secrets are skipped. Community swarms are
// tagged so the engine can keep untrusted peers out of them.
func (c Config) DesiredSwarms() []DesiredSwarm {
	seen := make(map[metainfo.Hash]bool)
	var out []DesiredSwarm
	add := func(h metainfo.Hash, ok, community bool) {
		if ok && !seen[h] {
			seen[h] = true
			out = append(out, DesiredSwarm{Hash: h, Community: community})
		}
	}
	if c.Global {
		add(GlobalInfoHash(), true, false)
	}
	for _, t := range c.Topics {
		h, ok := TopicInfoHash(t)
		add(h, ok, false)
	}
	for _, s := range c.Communities {
		h, ok := CommunityInfoHash(s)
		add(h, ok, true)
	}
	return out
}

// Manager keeps the engine's joined rendezvous swarms in sync with a Config. It
// is deterministic control logic — it owns the rendezvous set, so state
// transitions come from Reconcile, never from prompt/LLM behaviour.
type Manager struct {
	j   Joiner
	cfg Config
	log *slog.Logger
}

// NewManager builds a Manager. log may be nil (defaults to slog.Default()).
func NewManager(j Joiner, cfg Config, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{j: j, cfg: cfg, log: log}
}

// Reconcile makes the engine's joined swarms exactly match the desired set:
// joins the ones missing and leaves the ones no longer desired. Idempotent —
// AddRendezvous/RemoveRendezvous are both no-ops when already in the target
// state — so it is safe to call repeatedly. Individual add/remove errors are
// logged and skipped rather than aborting the whole reconcile.
func (m *Manager) Reconcile() {
	desired := m.cfg.DesiredSwarms()
	desiredSet := make(map[metainfo.Hash]bool, len(desired))
	for _, d := range desired {
		desiredSet[d.Hash] = true
	}

	current := make(map[metainfo.Hash]bool)
	for _, hexIH := range m.j.RendezvousInfoHashes() {
		var h metainfo.Hash
		if err := h.FromHexString(hexIH); err == nil {
			current[h] = true
		}
	}

	for _, d := range desired {
		if current[d.Hash] {
			continue
		}
		if err := m.j.AddRendezvous(d.Hash, d.Community); err != nil {
			m.log.Warn("rendezvous.join_failed", "swarm", shortHash(d.Hash), "err", err)
		}
	}
	for h := range current {
		if desiredSet[h] {
			continue
		}
		if err := m.j.RemoveRendezvous(h); err != nil {
			m.log.Warn("rendezvous.leave_failed", "swarm", shortHash(h), "err", err)
		}
	}
}

// shortHash is a non-reversible 8-hex-char label for a rendezvous swarm, safe to
// log: a PRIVATE community infohash is the group's enumeration capability (a full
// hex infohash lets anyone get_peers it on the DHT), so we never log it in full —
// 4 bytes is enough to correlate log lines but not to compute the DHT target.
func shortHash(h metainfo.Hash) string { return h.HexString()[:8] + "…" }

// Run reconciles once immediately, then re-reconciles every interval until ctx
// is cancelled. interval <= 0 reconciles once and returns (no background loop).
func (m *Manager) Run(ctx context.Context, interval time.Duration) {
	m.Reconcile()
	if interval <= 0 {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.Reconcile()
		}
	}
}
