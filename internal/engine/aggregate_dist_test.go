package engine

import (
	"context"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/swartznet/swartznet/contracts/dhtschema"
	"github.com/swartznet/swartznet/contracts/snagg"
)

// buildAggTestTree builds a tiny signed SNAGG tree with one "ubuntu" record and
// returns (bytes, fingerprint) for the adapter tests.
func buildAggTestTree(t *testing.T) ([]byte, [32]byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	var pubArr [32]byte
	copy(pubArr[:], pub)
	var r snagg.Record
	r.Pk = pubArr
	r.Kw = "ubuntu"
	for i := range r.Ih {
		r.Ih[i] = 0x40 + byte(i)
	}
	r.T = 1700000000
	copy(r.Sig[:], ed25519.Sign(priv, r.SigMessage()))
	built, err := snagg.BuildBTree(snagg.BuildInput{
		Records: []snagg.Record{r}, PubKey: pubArr, PrivKey: priv,
		Seq: 1, CreatedTs: 1700000000, PieceSize: snagg.MinPieceSize,
	})
	if err != nil {
		t.Fatal(err)
	}
	return built.Bytes, built.Fingerprint
}

type fakeSeeder struct {
	seededMI   *metainfo.MetaInfo
	seededPath string
	dropped    [][20]byte
}

func (f *fakeSeeder) SeedMetaInfo(mi *metainfo.MetaInfo, contentPath string) error {
	f.seededMI, f.seededPath = mi, contentPath
	return nil
}
func (f *fakeSeeder) DropCompanionTorrent(ih [20]byte) error {
	f.dropped = append(f.dropped, ih)
	return nil
}

type fakePutter struct{ got dhtschema.PPMIValue }

func (f *fakePutter) PutPPMI(_ context.Context, v dhtschema.PPMIValue) error {
	f.got = v
	return nil
}

// TestAggTreePublisherWritesSeedsPuts proves the live publisher writes the tree
// to the companion dir, seeds the wrapped torrent, and puts a PPMI pointer whose
// infohash matches the seeded torrent and whose commit matches the fingerprint.
func TestAggTreePublisherWritesSeedsPuts(t *testing.T) {
	t.Parallel()
	data, fp := buildAggTestTree(t)
	dir := t.TempDir()
	seeder := &fakeSeeder{}
	putter := &fakePutter{}
	p := &aggTreePublisher{seeder: seeder, putter: putter, dir: dir}

	name := "swartznet-aggregate-test.snagg"
	if err := p.PublishTree(context.Background(), name, data, fp); err != nil {
		t.Fatalf("PublishTree: %v", err)
	}

	// The bytes were written to the companion dir.
	onDisk, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("tree not written: %v", err)
	}
	if len(onDisk) != len(data) {
		t.Errorf("wrote %d bytes, want %d", len(onDisk), len(data))
	}
	// The seeded torrent's infohash == the pointer's infohash == the commit binds.
	if seeder.seededMI == nil {
		t.Fatal("nothing seeded")
	}
	seededIH := seeder.seededMI.HashInfoBytes()
	if string(putter.got.IH) != string(seededIH[:]) {
		t.Error("PPMI infohash != seeded torrent infohash")
	}
	if string(putter.got.Commit) != string(fp[:]) {
		t.Error("PPMI commit != tree fingerprint")
	}
	if seeder.seededPath != filepath.Join(dir, name) {
		t.Errorf("seeded from %q, want %q", seeder.seededPath, filepath.Join(dir, name))
	}
	// First publish drops nothing (no previous seed).
	if len(seeder.dropped) != 0 {
		t.Errorf("first publish dropped %d seeds, want 0", len(seeder.dropped))
	}

	// A second publish of DIFFERENT bytes drops the first seed.
	data2, fp2 := buildAggTestTree(t)
	if err := p.PublishTree(context.Background(), name, data2, fp2); err != nil {
		t.Fatalf("second PublishTree: %v", err)
	}
	if len(seeder.dropped) != 1 {
		t.Fatalf("second publish dropped %d seeds, want 1", len(seeder.dropped))
	}
	if seeder.dropped[0] != seededIH {
		t.Error("second publish dropped the wrong (not the previous) seed")
	}

	// RetractTree drops the current seed (retract-to-empty), then is idempotent.
	if err := p.RetractTree(context.Background()); err != nil {
		t.Fatalf("RetractTree: %v", err)
	}
	if len(seeder.dropped) != 2 {
		t.Fatalf("RetractTree dropped %d seeds total, want 2", len(seeder.dropped))
	}
	if err := p.RetractTree(context.Background()); err != nil {
		t.Fatalf("second RetractTree: %v", err)
	}
	if len(seeder.dropped) != 2 {
		t.Errorf("idempotent RetractTree dropped again (total %d, want 2)", len(seeder.dropped))
	}
}

// TestAggTreeResolverSkipsSelf proves the resolver short-circuits a lookup for
// the node's OWN pubkey — returning no hits WITHOUT fetching, so it never tears
// down the node's own aggregate seed (the HIGH review finding). A non-self
// pubkey still resolves normally.
func TestAggTreeResolverSkipsSelf(t *testing.T) {
	t.Parallel()
	self := [32]byte{0xEE, 0x11}
	fetch := &fakeFetcher{path: "/should/not/be/read"}
	get := &fakeGetter{v: dhtschema.PPMIValue{IH: make([]byte, 20)}}
	r := &aggTreeResolver{fetcher: fetch, getter: get, self: func() [32]byte { return self }}

	tree, err := r.ResolveTree(context.Background(), self)
	if err != nil || tree != nil {
		t.Errorf("self-lookup = (%v, %v); want (nil, nil)", tree, err)
	}
	if get.calls != 0 || fetch.got != ([20]byte{}) {
		t.Error("self-lookup touched the DHT/fetch path (would drop the node's own seed)")
	}

	// A different publisher is still resolved (getter consulted).
	other := [32]byte{0xAB}
	_, _ = r.ResolveTree(context.Background(), other)
	if get.calls != 1 {
		t.Errorf("non-self lookup did not consult the getter (calls=%d)", get.calls)
	}
}

type fakeFetcher struct {
	path string
	err  error
	got  [20]byte
}

func (f *fakeFetcher) FetchCompanionTorrent(_ context.Context, ih [20]byte) (string, error) {
	f.got = ih
	return f.path, f.err
}

type fakeGetter struct {
	v     dhtschema.PPMIValue
	err   error
	calls int
}

func (f *fakeGetter) GetPPMI(_ context.Context, _ [32]byte) (dhtschema.PPMIValue, error) {
	f.calls++
	return f.v, f.err
}

// TestAggTreeResolverFetchesAndVerifies proves the live resolver reads the
// pointer, fetches the named companion torrent, and opens a commit-verified
// tree — and that a commit mismatch is rejected.
func TestAggTreeResolverFetchesAndVerifies(t *testing.T) {
	t.Parallel()
	data, fp := buildAggTestTree(t)
	path := filepath.Join(t.TempDir(), "fetched.snagg")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	ih := [20]byte{0x01, 0x02, 0x03}

	// Happy path: pointer commit matches the tree → resolves + queries.
	r := &aggTreeResolver{
		fetcher: &fakeFetcher{path: path},
		getter:  &fakeGetter{v: dhtschema.PPMIValue{IH: ih[:], Commit: fp[:]}},
	}
	tree, err := r.ResolveTree(context.Background(), [32]byte{0xAA})
	if err != nil {
		t.Fatalf("ResolveTree: %v", err)
	}
	hits, err := tree.Find("ubuntu")
	if err != nil || len(hits) != 1 {
		t.Errorf("Find(ubuntu) = %d hits, err %v; want 1", len(hits), err)
	}

	// Commit mismatch → rejected (the pointer→tree binding survives the fetch).
	wrong := make([]byte, 32)
	rBad := &aggTreeResolver{
		fetcher: &fakeFetcher{path: path},
		getter:  &fakeGetter{v: dhtschema.PPMIValue{IH: ih[:], Commit: wrong}},
	}
	if _, err := rBad.ResolveTree(context.Background(), [32]byte{0xAA}); err == nil {
		t.Error("commit-mismatched tree accepted")
	}

	// Getter error → propagated.
	rErr := &aggTreeResolver{getter: &fakeGetter{err: os.ErrNotExist}}
	if _, err := rErr.ResolveTree(context.Background(), [32]byte{0xAA}); err == nil {
		t.Error("getter error not propagated")
	}

	// Malformed pointer (bad IH width) → error, no fetch attempted.
	ff := &fakeFetcher{path: path}
	rShort := &aggTreeResolver{fetcher: ff, getter: &fakeGetter{v: dhtschema.PPMIValue{IH: []byte{0x01}, Commit: fp[:]}}}
	if _, err := rShort.ResolveTree(context.Background(), [32]byte{0xAA}); err == nil {
		t.Error("short-infohash pointer accepted")
	}
	if ff.got != ([20]byte{}) {
		t.Error("fetch attempted despite a malformed pointer")
	}
}
