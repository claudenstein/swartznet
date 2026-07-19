package dhtindex

import (
	"context"
	"crypto/ed25519"
	"errors"
	"sync"
	"testing"

	"github.com/swartznet/swartznet/contracts/dhtschema"
	"github.com/swartznet/swartznet/contracts/snagg"
)

// memTreeNet is an in-memory stand-in for the DHT PPMI pointer + companion blob
// store: a publisher deposits its tree under its own pubkey; a resolver fetches
// and commit-verifies it by that pubkey. It lets these tests drive the
// aggregatePPMI distribute→resolve round-trip through the real WrapSnaggTorrent
// / OpenVerifiedTree seam without a live engine or DHT.
type memTreeNet struct {
	mu    sync.Mutex
	blobs map[[32]byte]memBlob
	fail  bool // when set, ResolveTree returns a transport error
}

type memBlob struct {
	bytes  []byte
	commit [32]byte
}

func newMemTreeNet() *memTreeNet { return &memTreeNet{blobs: make(map[[32]byte]memBlob)} }

func (n *memTreeNet) put(owner [32]byte, b memBlob) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.blobs[owner] = b
}

func (n *memTreeNet) get(owner [32]byte) (memBlob, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	b, ok := n.blobs[owner]
	return b, ok
}

func (n *memTreeNet) del(owner [32]byte) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.blobs, owner)
}

// memPublisher is the TreePublisher half: it wraps the tree as a companion
// torrent (proving the bytes are piece-aligned) and deposits them under its
// owner pubkey, mirroring the engine adapter that seeds + puts the PPMI pointer.
type memPublisher struct {
	net      *memTreeNet
	owner    [32]byte
	calls    int
	retracts int
}

func (p *memPublisher) PublishTree(_ context.Context, name string, snaggBytes []byte, commit [32]byte) error {
	if _, err := WrapSnaggTorrent(name, snaggBytes); err != nil {
		return err
	}
	p.calls++
	p.net.put(p.owner, memBlob{bytes: append([]byte(nil), snaggBytes...), commit: commit})
	return nil
}

func (p *memPublisher) RetractTree(_ context.Context) error {
	p.retracts++
	p.net.del(p.owner)
	return nil
}

// memResolver is the TreeResolver half: it fetches the blob for a pubkey and
// opens it as a commit-verified tree, mirroring the engine adapter that reads
// the PPMI pointer, fetches the companion torrent, and OpenVerifiedTree's it.
type memResolver struct{ net *memTreeNet }

func (r *memResolver) ResolveTree(_ context.Context, pubkey [32]byte) (*snagg.Tree, error) {
	if r.net.fail {
		return nil, errors.New("resolve transport failure")
	}
	b, ok := r.net.get(pubkey)
	if !ok {
		return nil, nil
	}
	return OpenVerifiedTree(b.bytes, b.commit[:])
}

func mustPub(t *testing.T) (ed25519.PrivateKey, [32]byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	var arr [32]byte
	copy(arr[:], pub)
	return priv, arr
}

// ihFor returns a deterministic 20-byte infohash for a seed byte.
func ihFor(seed byte) []byte {
	ih := make([]byte, 20)
	for i := range ih {
		ih[i] = seed + byte(i)
	}
	return ih
}

// TestAggregateDistributeResolveRoundTrip is the backend-level cross-publisher
// e2e: node A builds + distributes its SNAGG tree on Refresh; node B, holding
// only A's pubkey and a resolver, answers a lookup for A's keyword from A's
// resolved tree. It proves the two ports compose end-to-end.
func TestAggregateDistributeResolveRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	net := newMemTreeNet()

	// Node A: a real signer that publishes "ubuntu"→ihA and distributes.
	privA, pubA := mustPub(t)
	a := newAggregatePPMI(privA, pubA, nil)
	pubPort := &memPublisher{net: net, owner: pubA}
	a.SetDistribution(pubPort, nil)
	ihA := ihFor(0x40)
	if err := a.Publish(ctx, []string{"ubuntu"}, dhtschema.KeywordHit{IH: ihA}); err != nil {
		t.Fatal(err)
	}
	if err := a.Refresh(ctx); err != nil {
		t.Fatalf("A.Refresh (distribute): %v", err)
	}
	if pubPort.calls != 1 {
		t.Fatalf("PublishTree called %d times, want 1", pubPort.calls)
	}

	// The deposited pointer commit must equal the tree's real fingerprint.
	blob, ok := net.get(pubA)
	if !ok {
		t.Fatal("A distributed nothing")
	}
	built, err := snagg.OpenBTree(snagg.BytesPageSource{Data: blob.bytes, PieceSize: snagg.MinPieceSize})
	if err != nil {
		t.Fatalf("reopen distributed tree: %v", err)
	}
	if built.Trailer.Fingerprint != blob.commit {
		t.Error("distributed PPMI commit != tree fingerprint")
	}

	// Node B: no signer of its own, only a resolver. It answers A's keyword.
	b := newAggregatePPMI(nil, [32]byte{0xBB}, nil)
	b.SetDistribution(nil, &memResolver{net: net})
	hits, err := b.Lookup(ctx, pubA, "ubuntu")
	if err != nil {
		t.Fatalf("B.Lookup(A, ubuntu): %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("cross-publisher lookup returned %d hits, want 1", len(hits))
	}
	if string(hits[0].IH) != string(ihA) {
		t.Error("resolved hit infohash does not match what A published")
	}

	// A miss keyword resolves the tree but finds nothing.
	miss, err := b.Lookup(ctx, pubA, "fedora")
	if err != nil || len(miss) != 0 {
		t.Errorf("miss lookup = %d hits, err %v; want 0, nil", len(miss), err)
	}
}

// TestAggregateResolveToleratesFailure proves a cross-publisher lookup degrades
// to "no hits" (never an error) when the resolver is absent, the publisher is
// unknown, or the transport fails — one unreachable publisher can't fail a query.
func TestAggregateResolveToleratesFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	net := newMemTreeNet()
	_, pubA := mustPub(t)

	// No resolver wired ⇒ no hits, no error.
	bNil := newAggregatePPMI(nil, [32]byte{0x01}, nil)
	if hits, err := bNil.Lookup(ctx, pubA, "ubuntu"); err != nil || hits != nil {
		t.Errorf("nil-resolver lookup = %v, %v; want nil, nil", hits, err)
	}

	// Resolver present but publisher unknown ⇒ no hits, no error.
	b := newAggregatePPMI(nil, [32]byte{0x02}, nil)
	b.SetDistribution(nil, &memResolver{net: net})
	if hits, err := b.Lookup(ctx, pubA, "ubuntu"); err != nil || hits != nil {
		t.Errorf("unknown-publisher lookup = %v, %v; want nil, nil", hits, err)
	}

	// Transport failure ⇒ tolerated (no hits, no error).
	net.fail = true
	if hits, err := b.Lookup(ctx, pubA, "ubuntu"); err != nil || hits != nil {
		t.Errorf("failing-transport lookup = %v, %v; want nil, nil", hits, err)
	}
}

// TestAggregateResolveRejectsMismatchedCommit proves the pointer→tree binding
// survives distribution: if the fetched bytes disagree with the deposited
// commit, OpenVerifiedTree rejects them and the lookup yields no hits (the
// caller is never handed records from a tree the pointer didn't vouch for).
func TestAggregateResolveRejectsMismatchedCommit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	net := newMemTreeNet()
	privA, pubA := mustPub(t)
	a := newAggregatePPMI(privA, pubA, nil)
	a.SetDistribution(&memPublisher{net: net, owner: pubA}, nil)
	if err := a.Publish(ctx, []string{"ubuntu"}, dhtschema.KeywordHit{IH: ihFor(0x40)}); err != nil {
		t.Fatal(err)
	}
	if err := a.Refresh(ctx); err != nil {
		t.Fatal(err)
	}

	// Tamper the deposited commit so it no longer matches the bytes.
	blob, _ := net.get(pubA)
	blob.commit[0] ^= 0xFF
	net.put(pubA, blob)

	b := newAggregatePPMI(nil, [32]byte{0x03}, nil)
	b.SetDistribution(nil, &memResolver{net: net})
	hits, err := b.Lookup(ctx, pubA, "ubuntu")
	if err != nil {
		t.Fatalf("lookup surfaced the binding failure as an error: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("returned %d hits from a commit-mismatched tree, want 0", len(hits))
	}
}

// TestAggregateRetractToEmptyDropsSeed proves that when the last record is
// retracted (the tree goes empty), the next Refresh calls RetractTree so the
// publisher stops serving a stale index — it does not silently keep the old
// seed + pointer alive.
func TestAggregateRetractToEmptyDropsSeed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	net := newMemTreeNet()
	privA, pubA := mustPub(t)
	a := newAggregatePPMI(privA, pubA, nil)
	pubPort := &memPublisher{net: net, owner: pubA}
	a.SetDistribution(pubPort, nil)

	var ih20 [20]byte
	copy(ih20[:], ihFor(0x40))
	if err := a.Publish(ctx, []string{"ubuntu"}, dhtschema.KeywordHit{IH: ih20[:]}); err != nil {
		t.Fatal(err)
	}
	if err := a.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := net.get(pubA); !ok {
		t.Fatal("first refresh distributed nothing")
	}

	// Retract the only record → tree goes empty → next Refresh must retract.
	if err := a.Retract(ctx, ih20); err != nil {
		t.Fatal(err)
	}
	if err := a.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if pubPort.retracts != 1 {
		t.Errorf("RetractTree called %d times, want 1", pubPort.retracts)
	}
	if _, ok := net.get(pubA); ok {
		t.Error("stale tree still distributed after retract-to-empty")
	}
}

// TestAggregateNoPublisherIsLocalOnly proves that without a TreePublisher,
// Refresh builds the tree (self-lookup works) but distributes nothing.
func TestAggregateNoPublisherIsLocalOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	privA, pubA := mustPub(t)
	a := newAggregatePPMI(privA, pubA, nil)
	if err := a.Publish(ctx, []string{"debian"}, dhtschema.KeywordHit{IH: ihFor(0x10)}); err != nil {
		t.Fatal(err)
	}
	if err := a.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	// Self-lookup still works locally.
	hits, err := a.Lookup(ctx, pubA, "debian")
	if err != nil || len(hits) != 1 {
		t.Errorf("local self-lookup = %d hits, err %v; want 1", len(hits), err)
	}
}
