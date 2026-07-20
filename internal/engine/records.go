package engine

import (
	"crypto/ed25519"
	"time"

	"github.com/swartznet/swartznet/contracts/record"
	"github.com/swartznet/swartznet/contracts/token"
	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// Record-cache lifecycle constants.
const (
	DefaultRecordCacheMax           = 100_000
	DefaultRecordCacheMaxAge        = 30 * 24 * time.Hour
	DefaultRecordCachePruneInterval = time.Hour
	regtestRecordPruneInterval      = 500 * time.Millisecond
	// aggregatePoWBits is the mining target for minted records. Zero this
	// slice (a dual-read migration window precedes raising it).
	aggregatePoWBits = 0
)

// RecordCache returns the Aggregate record cache (never nil after New).
func (e *Engine) RecordCache() *swarmsearch.RecordCache { return e.recCache }

// selfPubkey returns this node's identity pubkey, or zero before SetSigner. Used
// by the aggregate resolver to skip resolving the node's own tree.
func (e *Engine) selfPubkey() [32]byte {
	e.recMu.Lock()
	defer e.recMu.Unlock()
	return e.signerPub
}

// SetSigner installs the identity that mints Aggregate records. The daemon
// calls it BEFORE RestoreSession so restored torrents mint. It also wires the
// publisher pubkey the swarm gossips in peer_announce (only when publishing).
func (e *Engine) SetSigner(priv ed25519.PrivateKey, pub [32]byte) {
	e.recMu.Lock()
	e.signer = priv
	e.signerPub = pub
	e.hasSigner = len(priv) == ed25519.PrivateKeySize
	has := e.hasSigner
	e.recMu.Unlock()
	if e.swarm != nil {
		e.swarm.SetPublisherPubkey(pub, has && e.publishingActive())
	}
	// Bring up the Layer-D write side now that an identity is present. No-op
	// when the DHT is off or publishing is suppressed (leech-only Layer D).
	if has {
		e.setupLayerDPublisher(priv, pub)
	}
}

// mintAggregateRecords signs one record per torrent name-keyword and adds it to
// the cache. No-op without a signer or cache. PoW bits = 0 this slice.
func (e *Engine) mintAggregateRecords(h *Handle) {
	e.recMu.Lock()
	signer, pub, has := e.signer, e.signerPub, e.hasSigner
	e.recMu.Unlock()
	if !has || e.recCache == nil {
		return
	}
	ih := h.T.InfoHash()
	name := h.T.Name()
	now := time.Now().Unix()
	minted := 0
	for _, kw := range token.TokenizeAll(name) {
		if len(kw) < 1 || len(kw) > record.MaxKeywordBytes {
			continue
		}
		r, err := record.SignAndMine(signer, pub, kw, ih, now, aggregatePoWBits)
		if err != nil {
			continue
		}
		e.recCache.Add(r)
		minted++
	}
	if minted > 0 {
		e.log.Debug("engine.records_minted", "info_hash", h.InfoHashHex(), "keywords", minted)
	}
}

// runRecordPrune periodically drops records older than DefaultRecordCacheMaxAge,
// bounding cache growth from long-lived reconciliation.
func (e *Engine) runRecordPrune() {
	interval := DefaultRecordCachePruneInterval
	if e.cfg.Regtest {
		interval = regtestRecordPruneInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-e.bgCtx.Done():
			return
		case <-ticker.C:
			if e.recCache != nil {
				cutoff := time.Now().Add(-DefaultRecordCacheMaxAge).Unix()
				if n := e.recCache.PruneOlderThan(cutoff); n > 0 {
					e.log.Debug("engine.records_pruned", "removed", n)
				}
			}
		}
	}
}
