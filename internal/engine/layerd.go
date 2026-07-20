package engine

import (
	"crypto/ed25519"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// This file owns the engine's Layer-D (BEP-44 keyword index) wiring. The read
// side (Lookup) is built in New when the DHT is enabled and stays alive even
// when the node never publishes (leech-only Layer D). The write side
// (Publisher) is built lazily in SetSigner, only when an identity is present
// AND publishing is active — --no-index / --no-dht-publish keep the read/pointer
// side alive but suppress network-visible publication under the user's key.

// setupLayerDLookup builds the read side. A nil DHT server (DisableDHT) leaves
// dhtLookup nil, so the daemon wires no Layer-D searcher.
func (e *Engine) setupLayerDLookup() {
	srv := e.dhtServer()
	if srv == nil {
		return
	}
	getter, err := dhtindex.NewAnacrolixGetter(srv)
	if err != nil {
		e.log.Warn("engine.layerd.getter_err", "err", err)
		return
	}
	// Read-only backend: getter + in-memory manifest, no putter. Lookup only
	// ever calls backend.Lookup, which touches the getter alone.
	mem, _ := dhtindex.LoadOrCreateManifest("")
	readBackend, err := dhtindex.NewBackend(e.cfg.LayerDMode, nil, nil, getter, mem, dhtindex.PublisherOptions{}, e.log)
	if err != nil {
		e.log.Warn("engine.layerd.backend_err", "err", err)
		return
	}
	// Aggregate/composite read side: wire the resolver so cross-publisher
	// lookups can pull another publisher's SNAGG tree (PPMI pointer → companion
	// torrent → commit-verified tree). A legacy backend does not implement
	// DistributableBackend, so this is a no-op there (ship default stays inert).
	if d, ok := readBackend.(dhtindex.DistributableBackend); ok {
		d.SetDistribution(nil, &aggTreeResolver{fetcher: e, getter: getter, self: e.selfPubkey})
	}
	lk := dhtindex.NewLookup(readBackend)
	e.repMu.Lock()
	lk.SetTracker(e.tracker)
	lk.SetBloom(e.bloom)
	lk.SetSourceTracker(e.sources)
	e.repMu.Unlock()
	lk.SetMinIndexerScore(e.cfg.MinIndexerScore)

	e.dhtMu.Lock()
	e.dhtLookup = lk
	e.dhtMu.Unlock()

	// Gossiped publisher pubkeys (peer_announce records) enter the lookup set.
	if e.swarm != nil {
		e.swarm.SetPublisherObserver(lk)
	}
}

// setupLayerDPublisher builds the write side. Called from SetSigner. No-op
// when the DHT is off, publishing is suppressed, or a publisher already exists.
func (e *Engine) setupLayerDPublisher(priv ed25519.PrivateKey, pub [32]byte) {
	srv := e.dhtServer()
	if srv == nil || !e.publishingActive() {
		return
	}
	e.dhtMu.Lock()
	alreadyBuilt := e.dhtPublisher != nil
	lookup := e.dhtLookup
	e.dhtMu.Unlock()
	// Self-pubkey is always added to the lookup set once we have an identity,
	// so this node's own published hits are discoverable to its own searches.
	if lookup != nil {
		lookup.AddIndexer(pub, "self")
	}
	if alreadyBuilt {
		return
	}

	putter, err := dhtindex.NewAnacrolixPutter(srv, priv)
	if err != nil {
		e.log.Warn("engine.layerd.putter_err", "err", err)
		return
	}
	getter, err := dhtindex.NewAnacrolixGetter(srv)
	if err != nil {
		e.log.Warn("engine.layerd.publisher_getter_err", "err", err)
		return
	}
	manifest, err := dhtindex.LoadOrCreateManifest(e.cfg.PublisherPath)
	if err != nil {
		e.log.Warn("engine.layerd.manifest_err", "err", err)
		return
	}
	opts := dhtindex.DefaultPublisherOptions()
	if e.cfg.Regtest {
		opts = dhtindex.RegtestPublisherOptions()
	}
	backend, err := dhtindex.NewBackend(e.cfg.LayerDMode, priv, putter, getter, manifest, opts, e.log)
	if err != nil {
		e.log.Warn("engine.layerd.backend_err", "err", err)
		return
	}
	// Aggregate/composite write side: wire the publisher so a rebuilt SNAGG tree
	// is seeded as a companion torrent + advertised via a PPMI pointer on each
	// refresh. Legacy backends don't implement DistributableBackend (no-op).
	if d, ok := backend.(dhtindex.DistributableBackend); ok {
		d.SetDistribution(&aggTreePublisher{seeder: e, putter: putter, dir: e.cfg.CompanionDir}, nil)
	}
	publisher := dhtindex.NewPublisher(backend, opts, e.log)
	publisher.Start()

	e.dhtMu.Lock()
	e.dhtPublisher = publisher
	e.dhtMu.Unlock()
	e.log.Info("engine.layerd.publisher_started", "pubkey", pub, "mode", e.cfg.LayerDMode)
}

// publishTorrent submits a torrent's NAME-keyword hit to the Layer-D publisher.
// No-op without an active publisher. Content tokens NEVER reach Layer D — the
// Publisher tokenizes the name only.
func (e *Engine) publishTorrent(h *Handle) {
	e.dhtMu.Lock()
	pub := e.dhtPublisher
	e.dhtMu.Unlock()
	if pub == nil {
		return
	}
	t := h.T
	ih := t.InfoHash()
	pub.Submit(dhtindex.PublishTask{
		InfoHash:  ih[:],
		Name:      t.Name(),
		Seeders:   0,
		FileCount: len(t.Files()),
		SizeBytes: t.Length(),
	})
}

// DHTLookup returns the Layer-D read side, or nil when the DHT is off. The
// daemon adapts it into searchmux.
func (e *Engine) DHTLookup() *dhtindex.Lookup {
	e.dhtMu.Lock()
	defer e.dhtMu.Unlock()
	return e.dhtLookup
}

// DHTPublisher returns the Layer-D write side, or nil when the node is not
// publishing.
func (e *Engine) DHTPublisher() *dhtindex.Publisher {
	e.dhtMu.Lock()
	defer e.dhtMu.Unlock()
	return e.dhtPublisher
}

// PublisherStatus reports the Layer-D publish state (empty when not publishing).
func (e *Engine) PublisherStatus() dhtindex.PublisherStatus {
	e.dhtMu.Lock()
	pub := e.dhtPublisher
	e.dhtMu.Unlock()
	if pub == nil {
		return dhtindex.PublisherStatus{}
	}
	return pub.Status()
}
