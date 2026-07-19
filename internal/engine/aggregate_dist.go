package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/swartznet/swartznet/contracts/dhtschema"
	"github.com/swartznet/swartznet/contracts/snagg"
	"github.com/swartznet/swartznet/internal/dhtindex"
)

// This file owns the engine's live adapters for the Aggregate Layer-D path
// (LayerDMode=aggregatePPMI/composite). They implement the two narrow
// dhtindex ports — TreePublisher (distribute a rebuilt SNAGG tree) and
// TreeResolver (resolve another publisher's tree) — by composing primitives
// that each already carry their own tests: WrapSnaggTorrent / OpenVerifiedTree
// (dhtindex), SeedMetaInfo / FetchCompanionTorrent (companion.go), and
// PutPPMI / GetPPMI (ppmi_dht.go). The adapters depend on the interfaces below
// rather than *Engine directly, so the glue is unit-testable with fakes; the
// live wiring in layerd.go passes the engine, which satisfies all of them.

// ppmiPutter publishes a PPMI pointer under this node's identity key.
type ppmiPutter interface {
	PutPPMI(ctx context.Context, v dhtschema.PPMIValue) error
}

// ppmiGetter resolves another publisher's PPMI pointer by their pubkey.
type ppmiGetter interface {
	GetPPMI(ctx context.Context, pubkey [32]byte) (dhtschema.PPMIValue, error)
}

// treeSeeder seeds a wrapped SNAGG tree as a companion torrent and drops a
// stale one. *Engine satisfies it via SeedMetaInfo / DropCompanionTorrent.
type treeSeeder interface {
	SeedMetaInfo(mi *metainfo.MetaInfo, contentPath string) error
	DropCompanionTorrent(ih [20]byte) error
}

// treeFetcher fetches a companion torrent by infohash, returning its on-disk
// path. *Engine satisfies it via FetchCompanionTorrent.
type treeFetcher interface {
	FetchCompanionTorrent(ctx context.Context, infohash [20]byte) (string, error)
}

// aggTreePublisher is the live dhtindex.TreePublisher. On each rebuild it writes
// the SNAGG bytes into the companion dir, seeds them as a companion torrent, and
// puts the PPMI pointer (infohash + commit). It drops the previous snapshot's
// seed so aggregate seeds don't accumulate (mirrors the companion publisher).
type aggTreePublisher struct {
	seeder treeSeeder
	putter ppmiPutter
	dir    string

	mu           sync.Mutex
	lastSeededIH [20]byte
}

// PublishTree wraps → writes → seeds → drops-previous → puts the pointer.
func (p *aggTreePublisher) PublishTree(ctx context.Context, name string, snaggBytes []byte, commit [32]byte) error {
	mi, err := dhtindex.WrapSnaggTorrent(name, snaggBytes)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return fmt.Errorf("engine: aggregate dir: %w", err)
	}
	path := filepath.Join(p.dir, name)
	if err := os.WriteFile(path, snaggBytes, 0o644); err != nil {
		return fmt.Errorf("engine: write aggregate tree: %w", err)
	}
	if err := p.seeder.SeedMetaInfo(mi, path); err != nil {
		return fmt.Errorf("engine: seed aggregate tree: %w", err)
	}
	ih := mi.HashInfoBytes()
	var ihArr [20]byte
	copy(ihArr[:], ih[:])
	// Drop the previous snapshot's seed (its infohash differs because the bytes
	// changed) so aggregate seeds do not pile up across refreshes.
	p.mu.Lock()
	prev := p.lastSeededIH
	p.lastSeededIH = ihArr
	p.mu.Unlock()
	if prev != ([20]byte{}) && prev != ihArr {
		_ = p.seeder.DropCompanionTorrent(prev)
	}
	return p.putter.PutPPMI(ctx, dhtschema.PPMIValue{IH: ih[:], Commit: commit[:]})
}

// RetractTree drops the seed for the last published snapshot (if any) so a
// publisher that retracted its final record stops serving a stale tree. The
// PPMI pointer is left to expire on its BEP-44 TTL.
func (p *aggTreePublisher) RetractTree(_ context.Context) error {
	p.mu.Lock()
	prev := p.lastSeededIH
	p.lastSeededIH = [20]byte{}
	p.mu.Unlock()
	if prev != ([20]byte{}) {
		return p.seeder.DropCompanionTorrent(prev)
	}
	return nil
}

// aggTreeResolver is the live dhtindex.TreeResolver. It reads a publisher's PPMI
// pointer, fetches the companion torrent it names, reads the bytes back, and
// opens them as a commit-verified tree (so the tree is bound to the signed
// pointer that led here).
type aggTreeResolver struct {
	fetcher treeFetcher
	getter  ppmiGetter
	// self reports this node's own identity pubkey (zero before an identity is
	// set). ResolveTree short-circuits a lookup for self: the node already has
	// its own hits via Layer L, and fetching its OWN seeded companion torrent
	// through FetchCompanionTorrent would tear that seed down on the deferred
	// drop AND read from the wrong path. Evaluated lazily so the read side, built
	// before SetSigner, still sees the identity at query time.
	self func() [32]byte
}

// ResolveTree does GetPPMI → FetchCompanionTorrent → ReadFile → OpenVerifiedTree,
// short-circuiting a self-lookup to no-hits.
func (r *aggTreeResolver) ResolveTree(ctx context.Context, pubkey [32]byte) (*snagg.Tree, error) {
	if r.self != nil {
		if sp := r.self(); sp != ([32]byte{}) && sp == pubkey {
			return nil, nil
		}
	}
	v, err := r.getter.GetPPMI(ctx, pubkey)
	if err != nil {
		return nil, err
	}
	if len(v.IH) != 20 {
		return nil, fmt.Errorf("engine: PPMI pointer has %d-byte infohash, want 20", len(v.IH))
	}
	var ih [20]byte
	copy(ih[:], v.IH)
	path, err := r.fetcher.FetchCompanionTorrent(ctx, ih)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return dhtindex.OpenVerifiedTree(data, v.Commit)
}

// Compile-time proof the adapters satisfy the dhtindex ports and that *Engine
// satisfies the seeder/fetcher interfaces the live wiring passes.
var (
	_ dhtindex.TreePublisher = (*aggTreePublisher)(nil)
	_ dhtindex.TreeResolver  = (*aggTreeResolver)(nil)
	_ treeSeeder             = (*Engine)(nil)
	_ treeFetcher            = (*Engine)(nil)
)
