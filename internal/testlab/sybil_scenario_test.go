package testlab_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/swartznet/swartznet/internal/dhtindex"
	"github.com/swartznet/swartznet/internal/reputation"
)

// TestScenarioSybilResistance exercises the Layer-D reputation
// system's ability to demote a malicious publisher. Three
// publishers share a keyword; one publishes garbage infohashes
// alongside real ones. After the querier flags the bad hits,
// the attacker's reputation drops below the MinIndexerScore
// threshold and subsequent lookups exclude the attacker's
// results.
//
// This scenario validates the full chain:
//   M5 reputation tracker → M9 source tracker →
//   M13c seed list weight → M15d PeerBook →
//   Lookup.Query MinIndexerScore pre-filter
//
// No containers, no real DHT, no real torrent — everything
// runs in-process via SharedMemoryStore.
func TestScenarioSybilResistance(t *testing.T) {
	// Three independent publisher keypairs.
	_, privAlice, _ := ed25519.GenerateKey(rand.Reader)
	_, privBob, _ := ed25519.GenerateKey(rand.Reader)
	_, privSybil, _ := ed25519.GenerateKey(rand.Reader)

	var pubAlice, pubBob, pubSybil [32]byte
	copy(pubAlice[:], privAlice.Public().(ed25519.PublicKey))
	copy(pubBob[:], privBob.Public().(ed25519.PublicKey))
	copy(pubSybil[:], privSybil.Public().(ed25519.PublicKey))

	// Shared "DHT".
	store := dhtindex.NewSharedMemoryStore()
	putA := store.PutterFor(privAlice)
	putB := store.PutterFor(privBob)
	putS := store.PutterFor(privSybil)

	ctx := context.Background()
	salt, _ := dhtindex.SaltForKeyword("ubuntu")

	// Alice and Bob publish the REAL infohash for "ubuntu".
	realIH := make([]byte, 20)
	for i := range realIH {
		realIH[i] = 0x11
	}
	realHit := dhtindex.KeywordHit{
		IH: realIH,
		N:  "ubuntu 24.04 real",
		S:  100,
	}
	_ = putA.Put(ctx, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{realHit}})
	_ = putB.Put(ctx, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{realHit}})

	// Sybil publishes a FAKE infohash alongside the real one.
	fakeIH := make([]byte, 20)
	for i := range fakeIH {
		fakeIH[i] = 0xff
	}
	fakeHit := dhtindex.KeywordHit{
		IH: fakeIH,
		N:  "ubuntu FAKE SPAM",
		S:  9999, // fake high seeders to lure clicks
	}
	_ = putS.Put(ctx, salt, dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{realHit, fakeHit}})

	// Build a Lookup with a reputation tracker wired in.
	tracker := reputation.NewTracker()
	lookup := dhtindex.NewLookup(store.Getter())
	lookup.AddIndexer(pubAlice, "alice")
	lookup.AddIndexer(pubBob, "bob")
	lookup.AddIndexer(pubSybil, "sybil")
	lookup.SetTracker(tracker)
	lookup.SetMinIndexerScore(0.3) // threshold below which indexers get skipped

	// First lookup: all three publishers are queried.
	resp, err := lookup.Query(ctx, "ubuntu")
	if err != nil {
		t.Fatalf("first query: %v", err)
	}
	if resp.IndexersAsked != 3 {
		t.Errorf("first query: asked %d, want 3", resp.IndexersAsked)
	}
	t.Logf("first query: asked=%d responded=%d hits=%d",
		resp.IndexersAsked, resp.IndexersResponded, len(resp.Hits))

	// The merged result should contain both infohashes: 0x11 (real)
	// from all three, and 0xff (fake) from sybil only.
	var foundReal, foundFake bool
	for _, h := range resp.Hits {
		if h.InfoHash[0:4] == "1111" {
			foundReal = true
		}
		if h.InfoHash[0:4] == "ffff" {
			foundFake = true
		}
	}
	if !foundReal {
		t.Error("first query missing real infohash")
	}
	if !foundFake {
		t.Error("first query missing fake infohash (expected — sybil hasn't been demoted yet)")
	}

	// Simulate realistic history: the sybil has been returning
	// many hits over multiple queries (a typical spammer floods
	// results), THEN the user flags its bad hits. With a large
	// returned count, the Bayesian smoothing lets the flag ratio
	// dominate — the prior weight (5) matters less when the
	// sample size is large.
	sybilPK := reputation.PubKey(pubSybil)
	tracker.RecordReturned(sybilPK, 50)   // simulate 50 hits seen from sybil
	for i := 0; i < 20; i++ {
		tracker.RecordFlagged(sybilPK)     // 20 flags out of 50 → raw = (0-20)/50 = 0
	}
	// Score: raw = max(0, 0-20)/50 = 0. Smoothed ≈ (0*50 + 0.5*5)/(50+5) = 0.045
	sybilScore := tracker.Score(sybilPK)
	t.Logf("sybil score after 20 flags on 50 returns: %.3f (threshold=0.3)", sybilScore)
	if sybilScore >= 0.3 {
		t.Errorf("sybil score %.3f still above threshold 0.3", sybilScore)
	}

	// Second lookup: the sybil should be SKIPPED because its
	// score is below MinIndexerScore. The result should only
	// contain the real infohash from alice + bob.
	resp2, err := lookup.Query(ctx, "ubuntu")
	if err != nil {
		t.Fatalf("second query: %v", err)
	}
	if resp2.IndexersAsked != 2 {
		t.Errorf("second query: asked %d, want 2 (sybil excluded)", resp2.IndexersAsked)
	}
	t.Logf("second query: asked=%d responded=%d hits=%d",
		resp2.IndexersAsked, resp2.IndexersResponded, len(resp2.Hits))

	// The fake infohash should no longer appear.
	for _, h := range resp2.Hits {
		if h.InfoHash[0:4] == "ffff" {
			t.Errorf("second query STILL contains fake infohash — sybil was not excluded")
		}
	}
	if resp2.IndexersResponded != 2 {
		t.Errorf("second query responded=%d, want 2", resp2.IndexersResponded)
	}

	// Sanity: alice and bob's scores should be neutral or better.
	aliceScore := tracker.Score(reputation.PubKey(pubAlice))
	bobScore := tracker.Score(reputation.PubKey(pubBob))
	t.Logf("alice=%.3f bob=%.3f sybil=%.3f", aliceScore, bobScore, sybilScore)
	if aliceScore < 0.3 {
		t.Errorf("alice score %.3f below threshold — collateral damage", aliceScore)
	}
	if bobScore < 0.3 {
		t.Errorf("bob score %.3f below threshold — collateral damage", bobScore)
	}
}
