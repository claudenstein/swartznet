package dhtindex

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/swartznet/swartznet/contracts/dhtschema"
	"github.com/swartznet/swartznet/contracts/record"
	"github.com/swartznet/swartznet/contracts/snagg"
)

// TreePublisher distributes a freshly rebuilt SNAGG tree: it wraps the bytes as
// a companion torrent, seeds them, and publishes the BEP-46/PPMI pointer so
// other nodes can find the tree by this publisher's pubkey. commit is the
// tree's fingerprint (the SNAGG trailer's Fingerprint), which the pointer binds
// to so a subscriber can reject a tree whose bytes disagree with the pointer.
// The engine supplies the live adapter; tests supply a fake. A nil publisher
// means "local-only" — the tree is built and self-served but never distributed.
type TreePublisher interface {
	PublishTree(ctx context.Context, name string, snaggBytes []byte, commit [32]byte) error
	// RetractTree is called when a rebuild leaves nothing to distribute (the
	// record set went empty): the publisher drops any seed it is still holding
	// so it does not keep serving a stale index. The BEP-44 pointer is left to
	// expire on its TTL (BEP-44 has no delete).
	RetractTree(ctx context.Context) error
}

// TreeResolver resolves another publisher's SNAGG tree from just their pubkey:
// it reads their PPMI pointer over the DHT, fetches the companion torrent it
// names, and opens the bytes as a commit-verified tree. The engine supplies the
// live adapter; tests supply a fake. A nil resolver means cross-publisher
// lookups return no hits (the local half still works).
type TreeResolver interface {
	ResolveTree(ctx context.Context, pubkey [32]byte) (*snagg.Tree, error)
}

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
	fp      [32]byte                  // fingerprint of the last built tree (PPMI commit)
	dirty   bool
	gen     uint64 // bumped every time rebuildLocked mutates tree (TOCTOU guard)

	// Distribution ports (nil ⇒ local-only). Set once at construction via
	// setDistribution before the backend goes live, so no lock is needed to
	// read them; snapshots under mu keep the race detector happy regardless.
	publisher TreePublisher
	resolver  TreeResolver
}

func newAggregatePPMI(priv ed25519.PrivateKey, pub [32]byte, log *slog.Logger) *aggregatePPMI {
	if log == nil {
		log = slog.Default()
	}
	return &aggregatePPMI{log: log, priv: priv, pub: pub, records: make(map[[32]byte]snagg.Record)}
}

// DistributableBackend is implemented by RecordBackends that can distribute
// their SNAGG tree over the DHT (as a companion torrent + PPMI pointer) and
// resolve other publishers' trees. The engine type-asserts NewBackend's result
// to this and wires live adapters — the write side gets a TreePublisher, the
// read side a TreeResolver. A legacy backend does not implement it, so the
// assert simply fails and nothing wires (ship default stays inert).
type DistributableBackend interface {
	// SetDistribution installs the distribute-on-refresh / resolve-on-lookup
	// ports. Either may be nil (the write side passes only a publisher, the read
	// side only a resolver).
	SetDistribution(pub TreePublisher, res TreeResolver)
}

// SetDistribution wires the optional distribute-on-refresh / resolve-on-lookup
// ports. Called by the engine when it supplies live adapters; left nil for a
// local-only or read-only instance. A later call overrides only the non-nil
// arguments, so the read and write sides can each wire their own half.
func (a *aggregatePPMI) SetDistribution(pub TreePublisher, res TreeResolver) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if pub != nil {
		a.publisher = pub
	}
	if res != nil {
		a.resolver = res
	}
}

// treeName is the stable companion-torrent filename for this publisher's tree.
// Deriving it from the pubkey keeps the wrapped infohash a pure function of the
// tree bytes, so re-seeding identical bytes yields an identical infohash.
func (a *aggregatePPMI) treeName() string {
	return "swartznet-aggregate-" + hex.EncodeToString(a.pub[:8]) + ".snagg"
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

// Refresh rebuilds the SNAGG tree if the record set changed, then — if a
// TreePublisher is wired — distributes the fresh bytes (seed + PPMI pointer).
// The distribute step runs off-lock: it snapshots the built tree + fingerprint
// under mu, then releases before the seed/DHT work so a slow put can't stall a
// concurrent Publish/Lookup. A distribute error is returned but the local tree
// is already updated, so self-lookup keeps working regardless.
func (a *aggregatePPMI) Refresh(ctx context.Context) error {
	a.mu.Lock()
	a.rebuildLocked()
	tree := a.tree
	fp := a.fp
	pub := a.publisher
	name := a.treeName()
	gen := a.gen
	a.mu.Unlock()
	if pub == nil {
		return nil
	}
	if err := a.distribute(ctx, pub, name, tree, fp); err != nil {
		return err
	}
	// TOCTOU guard: PublishTree/RetractTree run off-lock (a DHT put can take
	// seconds). A concurrent Retract may have rebuilt — even emptied — the tree
	// while we were distributing, so the bytes we just put could be stale (e.g. a
	// retracted infohash left discoverable). If the generation advanced,
	// re-distribute the CURRENT state once so the window shrinks from a full
	// refresh interval to a single extra put. A subsequent Retract is caught by
	// the next refresh tick.
	a.mu.Lock()
	tree2 := a.tree
	fp2 := a.fp
	changed := a.gen != gen
	a.mu.Unlock()
	if changed {
		return a.distribute(ctx, pub, name, tree2, fp2)
	}
	return nil
}

// distribute publishes a built tree, or retracts the seed when the tree is nil
// (records empty or read-only). Caller must NOT hold mu — the put runs off-lock.
func (a *aggregatePPMI) distribute(ctx context.Context, pub TreePublisher, name string, tree []byte, fp [32]byte) error {
	if tree == nil {
		// Nothing to distribute: drop any seed we still hold so a retracted-to-
		// empty publisher stops serving stale bytes.
		return pub.RetractTree(ctx)
	}
	if err := pub.PublishTree(ctx, name, tree, fp); err != nil {
		a.log.Warn("dhtindex.aggregate.distribute_err", "err", err)
		return err
	}
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
	a.gen++ // the tree is about to change (empty or rebuilt); signal Refresh's TOCTOU re-check
	if len(a.records) == 0 || a.priv == nil {
		a.tree = nil
		a.fp = [32]byte{}
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
		a.fp = [32]byte{}
		return
	}
	a.tree = built.Bytes
	a.fp = built.Fingerprint
}

// Lookup serves an exact-keyword query. A self-lookup (indexerPub == this
// node's pubkey) answers from the locally built tree. A cross-publisher lookup
// resolves the other publisher's tree via the TreeResolver port (PPMI pointer →
// companion torrent → commit-verified tree); with no resolver wired, or when a
// remote publisher can't be resolved right now, it returns no hits rather than
// an error — one unreachable publisher must not fail the whole query.
func (a *aggregatePPMI) Lookup(ctx context.Context, indexerPub [32]byte, token string) ([]dhtschema.KeywordHit, error) {
	if indexerPub != a.pub {
		a.mu.Lock()
		res := a.resolver
		a.mu.Unlock()
		if res == nil {
			return nil, nil
		}
		tree, err := res.ResolveTree(ctx, indexerPub)
		if err != nil {
			a.log.Debug("dhtindex.aggregate.resolve_err", "pub", hex.EncodeToString(indexerPub[:8]), "err", err)
			return nil, nil
		}
		if tree == nil {
			return nil, nil
		}
		return exactHits(tree, token)
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
	return exactHits(tree, token)
}

// exactHits runs a prefix Find and keeps only exact-keyword matches, mapping
// them to KeywordHits — the exact-keyword parity the legacy backend gives.
func exactHits(tree *snagg.Tree, token string) ([]dhtschema.KeywordHit, error) {
	recs, err := tree.Find(token)
	if err != nil {
		return nil, err
	}
	var hits []dhtschema.KeywordHit
	for _, r := range recs {
		if r.Kw != token {
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
