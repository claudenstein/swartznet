package dhtindex

import (
	"context"
	"crypto/ed25519"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/swartznet/swartznet/contracts/dhtschema"
	"github.com/swartznet/swartznet/contracts/record"
	"github.com/swartznet/swartznet/contracts/snagg"
)

// aggregatePPMI is the Aggregate RecordBackend: it accumulates signed
// keyword→infohash records, packs them into an in-memory signed SNAGG B-tree,
// and serves an authenticated prefix lookup from that tree. This is the LOCAL
// half of the Aggregate path — a publisher's own records, built + self-served.
// Cross-publisher discovery (resolving another publisher's PPMI pointer over
// the DHT and fetching their companion tree) is the crawler-adjacent piece
// deferred with the live distribution wiring; a read-only instance (nil signer)
// is inert. Reuses contracts/record for signing + contracts/snagg for the tree.
type aggregatePPMI struct {
	log  *slog.Logger
	priv ed25519.PrivateKey // nil ⇒ read-only (no local publishing)
	pub  [32]byte

	mu      sync.Mutex
	records map[[32]byte]snagg.Record // keyed by ElementID (dedup across re-signs)
	tree    []byte                    // built SNAGG file (nil until first build)
	dirty   bool
}

func newAggregatePPMI(priv ed25519.PrivateKey, pub [32]byte, log *slog.Logger) *aggregatePPMI {
	if log == nil {
		log = slog.Default()
	}
	return &aggregatePPMI{log: log, priv: priv, pub: pub, records: make(map[[32]byte]snagg.Record)}
}

// Publish signs one record per name-keyword for this node's own hit and adds it
// to the record set. A read-only instance (nil signer) is a no-op.
func (a *aggregatePPMI) Publish(_ context.Context, keywords []string, hit dhtschema.KeywordHit) error {
	if a.priv == nil || len(hit.IH) != 20 {
		return nil
	}
	var ih [20]byte
	copy(ih[:], hit.IH)
	now := time.Now().Unix()
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, kw := range keywords {
		kw = strings.ToLower(kw)
		if kw == "" || len(kw) > snagg.MaxKeywordBytes {
			continue
		}
		r, err := record.SignAndMine(a.priv, a.pub, kw, ih, now, 0)
		if err != nil {
			a.log.Warn("dhtindex.aggregate.sign_err", "kw", kw, "err", err)
			continue
		}
		a.records[r.ElementID()] = r
	}
	a.dirty = true
	return nil
}

// Refresh rebuilds the SNAGG tree if the record set changed.
func (a *aggregatePPMI) Refresh(_ context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rebuildLocked()
	return nil
}

// Retract removes every record for an infohash and rebuilds.
func (a *aggregatePPMI) Retract(_ context.Context, ih [20]byte) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	changed := false
	for id, r := range a.records {
		if r.Ih == ih {
			delete(a.records, id)
			changed = true
		}
	}
	if changed {
		a.dirty = true
		a.rebuildLocked()
	}
	return nil
}

// rebuildLocked (re)packs the record set into a signed SNAGG tree. Caller holds mu.
func (a *aggregatePPMI) rebuildLocked() {
	if !a.dirty {
		return
	}
	a.dirty = false
	if len(a.records) == 0 || a.priv == nil {
		a.tree = nil
		return
	}
	recs := make([]snagg.Record, 0, len(a.records))
	for _, r := range a.records {
		recs = append(recs, r)
	}
	built, err := snagg.BuildBTree(snagg.BuildInput{
		Records: recs, PubKey: a.pub, PrivKey: a.priv, Seq: 1, PieceSize: snagg.MinPieceSize, MinPoWBits: 0,
	})
	if err != nil {
		a.log.Warn("dhtindex.aggregate.build_err", "err", err)
		a.tree = nil
		return
	}
	a.tree = built.Bytes
}

// Lookup serves an exact-keyword query from this node's own SNAGG tree. Only
// self-lookup is wired locally (indexerPub must be this node's pubkey); other
// publishers' trees resolve via the deferred DHT-PPMI path.
func (a *aggregatePPMI) Lookup(_ context.Context, indexerPub [32]byte, token string) ([]dhtschema.KeywordHit, error) {
	if indexerPub != a.pub {
		return nil, nil
	}
	a.mu.Lock()
	a.rebuildLocked()
	treeBytes := a.tree
	a.mu.Unlock()
	if treeBytes == nil {
		return nil, nil
	}
	tree, err := snagg.OpenBTree(snagg.BytesPageSource{Data: treeBytes, PieceSize: snagg.MinPieceSize})
	if err != nil {
		return nil, err
	}
	recs, err := tree.Find(token)
	if err != nil {
		return nil, err
	}
	var hits []dhtschema.KeywordHit
	for _, r := range recs {
		if r.Kw != token { // Find is prefix; keep exact-keyword parity with legacy
			continue
		}
		hits = append(hits, dhtschema.KeywordHit{IH: append([]byte(nil), r.Ih[:]...)})
	}
	return hits, nil
}

// Status reports the local record set.
func (a *aggregatePPMI) Status() PublisherStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	kws := make(map[string]struct{})
	for _, r := range a.records {
		kws[r.Kw] = struct{}{}
	}
	return PublisherStatus{PubKey: a.pub, TotalKeywords: len(kws), TotalHits: len(a.records)}
}

func (a *aggregatePPMI) Close() error { return nil }
