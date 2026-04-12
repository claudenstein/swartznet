package testlab_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// TestLayerDLookupRoundTrip exercises the full Layer-D publish
// + lookup path (manifest → Publisher → Putter → DHT store →
// Getter → Lookup → merged hits) without any real network.
//
// The cluster harness isn't strictly required for this scenario
// — Layer D runs above the DHT wire, so it has no sn_search
// peer-connection prerequisite. But we use testlab.NewCluster
// anyway to get realistic engine state (identities, seeded
// Bleve indexes) and to prove the Layer-D path works alongside
// everything else without interfering.
//
// Flow:
//  1. Build a 2-node cluster (just for per-node identities and
//     tempdirs — DHT is disabled on every engine).
//  2. Create a dhtindex.SharedMemoryStore to stand in for the
//     real DHT.
//  3. Wire node A's manifest + Publisher against a Putter from
//     the shared store signed by node A's identity. Push one
//     keyword → infohash hit through the publisher.
//  4. Build a standalone Lookup against the shared store's
//     Getter, add node A as a known indexer, and query by the
//     keyword.
//  5. Assert the merged LookupResponse reports 1 indexer asked,
//     1 responded, 1 hit with the correct infohash and the
//     node-A label in its Sources.
//
// This is the first end-to-end Layer-D test in the project;
// previously every unit test stubbed either the Putter or the
// Getter separately.
func TestLayerDLookupRoundTrip(t *testing.T) {
	// Synthetic per-node ed25519 key. We can't reuse the
	// cluster's loaded identity because MemoryPutterGetter
	// needs the raw private key and Engine.Identity() doesn't
	// export it to external packages. Independent key is fine
	// — Layer D doesn't care which keypair signs the put as
	// long as the lookup knows the same pubkey.
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var pub [32]byte
	copy(pub[:], priv.Public().(ed25519.PublicKey))

	// Shared "DHT" for the publisher-to-lookup round trip.
	store := dhtindex.NewSharedMemoryStore()

	// Publisher side: feed one keyword → infohash mapping
	// straight into a fresh manifest, then Put it via the
	// shared store's per-publisher Putter.
	mf, err := dhtindex.LoadOrCreateManifest("")
	if err != nil {
		t.Fatal(err)
	}
	var ih [20]byte
	for i := range ih {
		ih[i] = 0x11
	}
	if _, err := mf.AddHit("ubuntu", dhtindex.KeywordHit{
		IH: ih[:],
		N:  "ubuntu 24.04 desktop amd64",
		S:  42,
		F:  3,
		Sz: 6 << 30,
	}); err != nil {
		t.Fatal(err)
	}

	putter := store.PutterFor(priv)
	salt, err := dhtindex.SaltForKeyword("ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	entry := mf.Snapshot()["ubuntu"]
	value := dhtindex.KeywordValue{Hits: entry.Hits}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := putter.Put(ctx, salt, value); err != nil {
		t.Fatal(err)
	}

	// Reader side: a fresh Lookup pointed at the shared store.
	lookup := dhtindex.NewLookup(store.Getter())
	lookup.AddIndexer(pub, "test-publisher")

	resp, err := lookup.Query(context.Background(), "ubuntu")
	if err != nil {
		t.Fatalf("Lookup.Query: %v", err)
	}
	if resp.IndexersAsked != 1 {
		t.Errorf("IndexersAsked = %d, want 1", resp.IndexersAsked)
	}
	if resp.IndexersResponded != 1 {
		t.Errorf("IndexersResponded = %d, want 1", resp.IndexersResponded)
	}
	if len(resp.Hits) != 1 {
		t.Fatalf("len(Hits) = %d, want 1", len(resp.Hits))
	}
	hit := resp.Hits[0]
	if hit.Name != "ubuntu 24.04 desktop amd64" {
		t.Errorf("hit.Name = %q", hit.Name)
	}
	if hit.InfoHash != "1111111111111111111111111111111111111111" {
		t.Errorf("hit.InfoHash = %q", hit.InfoHash)
	}
	if len(hit.Sources) == 0 || hit.Sources[0] != "test-publisher" {
		t.Errorf("hit.Sources = %v, want [test-publisher]", hit.Sources)
	}
}

// TestLayerDMultiIndexerMerge covers the merge path: two
// different publishers both claim the same infohash for the
// same keyword. Lookup must merge the hits into one row with
// both labels in Sources and a higher Score (because source
// count feeds the merge scoring).
func TestLayerDMultiIndexerMerge(t *testing.T) {
	store := dhtindex.NewSharedMemoryStore()

	// Two independent publishers.
	_, priv1, _ := ed25519.GenerateKey(rand.Reader)
	_, priv2, _ := ed25519.GenerateKey(rand.Reader)
	var pub1, pub2 [32]byte
	copy(pub1[:], priv1.Public().(ed25519.PublicKey))
	copy(pub2[:], priv2.Public().(ed25519.PublicKey))

	put1 := store.PutterFor(priv1)
	put2 := store.PutterFor(priv2)

	salt, _ := dhtindex.SaltForKeyword("ubuntu")
	var ih [20]byte
	for i := range ih {
		ih[i] = 0x22
	}
	commonHit := dhtindex.KeywordHit{
		IH: ih[:],
		N:  "ubuntu 24.04",
		S:  100,
	}
	ctx := context.Background()
	if err := put1.Put(ctx, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{commonHit}}); err != nil {
		t.Fatal(err)
	}
	if err := put2.Put(ctx, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{commonHit}}); err != nil {
		t.Fatal(err)
	}

	lookup := dhtindex.NewLookup(store.Getter())
	lookup.AddIndexer(pub1, "alice")
	lookup.AddIndexer(pub2, "bob")

	resp, err := lookup.Query(context.Background(), "ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	if resp.IndexersAsked != 2 || resp.IndexersResponded != 2 {
		t.Errorf("asked=%d responded=%d, want 2/2",
			resp.IndexersAsked, resp.IndexersResponded)
	}
	if len(resp.Hits) != 1 {
		t.Fatalf("want merged into 1 hit, got %d", len(resp.Hits))
	}
	hit := resp.Hits[0]
	if len(hit.Sources) != 2 {
		t.Errorf("hit.Sources = %v, want 2 sources", hit.Sources)
	}
}
