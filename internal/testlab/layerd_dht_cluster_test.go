package testlab_test

import (
	"context"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/dhtindex"
	"github.com/swartznet/swartznet/internal/testlab"
)

// TestLayerDDHTClusterRoundTrip is the in-process counterpart to
// testbed scenario s12. It stands up a full engine-wrapped
// Layer-D cluster on loopback — real identity, real Publisher,
// real Lookup, real anacrolix DHT, real getput.Put/Get — and
// asserts the seed's published keyword becomes visible on a
// different node's /search --dht-equivalent query.
//
// # Status (2026-04-23)
//
// This test reproduces the s12 docker failure in pure Go, on
// loopback. The put traversal reports NumAddrsTried=4 with
// NumResponses=4 and expvar "received put with valid token"
// increments on the receiver, yet a subsequent BEP-44 get
// returns "value not found" — matching the symptom in
// testbed/scenarios/s12-swarm-dht.sh's header. Proves the bug
// is NOT docker-specific; the engine's DHT wire path silently
// drops the stored item somewhere between token validation and
// bep44.Store. A raw-anacrolix 6-node loopback cluster
// (/tmp/dht6test.go, reproduced by
// internal/dhtindex.TestVanillaBEP44GetterReadsOurItem)
// succeeds at the same scenario, so the issue is specific to
// the engine's torrent-client-hosted DHT server (utpSocket
// wrapper, 0.0.0.0 default bind, or some other config
// difference the harness doesn't yet control).
//
// Skipped until the underlying bug is fixed. Once fixed,
// remove the Skip line — this test will then gate regressions
// on every PR.
func TestLayerDDHTClusterRoundTrip(t *testing.T) {
	t.Skip("known-failing: reproduces the s12 BEP-44 get/put bug in-process; " +
		"unblock once testbed/scenarios/s12-swarm-dht.sh's full put/get passes")

	const (
		nSeeds   = 2
		nLeeches = 4
		total    = nSeeds + nLeeches
		keyword  = "dhtcorpus"
	)

	c := testlab.NewDHTCluster(t, total)

	// Give DHTs time to cross-bootstrap before any put/get
	// traversal runs.
	time.Sleep(5 * time.Second)

	// Publish the same keyword from every seed, each pointing
	// at its own fixture infohash.
	fixtures := [nSeeds][20]byte{}
	for s := 0; s < nSeeds; s++ {
		for i := 0; i < 20; i++ {
			fixtures[s][i] = byte(0xA0 + s)
		}
		pub := c.Nodes[s].Eng.Publisher()
		if pub == nil {
			t.Fatalf("seed %d: Engine.Publisher() is nil", s)
		}
		pub.Submit(dhtindex.PublishTask{
			InfoHash: fixtures[s][:],
			Name:     keyword,
			Seeders:  1,
		})
	}
	if err := waitPublished(c, keyword, 20*time.Second); err != nil {
		t.Fatalf("seeds did not register a LastPublished for %q: %v", keyword, err)
	}

	// Leech-0 is the probe. Cross-register the seed pubkeys
	// as known indexers (shortcut for the sn_search gossip
	// path, which is tested independently).
	leech := c.Nodes[nSeeds]
	look := leech.Eng.Lookup()
	if look == nil {
		t.Fatalf("leech: Engine.Lookup() is nil")
	}
	for s := 0; s < nSeeds; s++ {
		id := c.Nodes[s].Eng.Identity()
		look.AddIndexer(id.PublicKeyBytes(), "seed")
	}

	deadline := time.Now().Add(15 * time.Second)
	var lastResp *dhtindex.LookupResponse
	for time.Now().Before(deadline) {
		qctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		resp, err := look.Query(qctx, keyword)
		cancel()
		if err == nil {
			lastResp = resp
			if resp.IndexersResponded >= 1 && len(resp.Hits) >= 1 {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	if lastResp == nil {
		t.Fatalf("Layer-D lookup never succeeded")
	}
	t.Fatalf("Layer-D lookup returned no hit: asked=%d responded=%d hits=%d",
		lastResp.IndexersAsked, lastResp.IndexersResponded, len(lastResp.Hits))
}

// TestDHTClusterPointerRoundTrip is a deeper-than-Layer-D
// diagnostic: it skips the keyword Publisher/Lookup machinery
// and drives the engine's BEP-46 PointerPutter/PointerGetter
// directly. Same underlying getput.Put/Get as the keyword
// path, so the failure surface is identical.
//
// Skipped alongside TestLayerDDHTClusterRoundTrip — if this
// one passes but the Layer-D test still fails, the bug is in
// Publisher/Lookup; if both fail, the bug is in the engine's
// DHT wire path (which today is what we observe).
func TestDHTClusterPointerRoundTrip(t *testing.T) {
	t.Skip("known-failing: same root cause as TestLayerDDHTClusterRoundTrip")

	const total = 6
	c := testlab.NewDHTCluster(t, total)
	time.Sleep(5 * time.Second)

	seed := c.Nodes[0]
	reader := c.Nodes[total-1]
	putter := seed.Eng.PointerPutter()
	getter := reader.Eng.PointerGetter()
	if putter == nil || getter == nil {
		t.Fatal("PointerPutter/PointerGetter is nil")
	}

	salt := []byte("testlab.pointer.roundtrip")
	var want [20]byte
	for i := range want {
		want[i] = 0xBE
	}

	putCtx, putCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer putCancel()
	if err := putter.PutInfohashPointer(putCtx, salt, want); err != nil {
		t.Fatalf("PutInfohashPointer: %v", err)
	}
	time.Sleep(time.Second)

	getCtx, getCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer getCancel()
	got, err := getter.GetInfohashPointer(getCtx, putter.PublicKey(), salt)
	if err != nil {
		t.Fatalf("GetInfohashPointer: %v", err)
	}
	if got != want {
		t.Errorf("pointer mismatch: got %x want %x", got, want)
	}
}

// waitPublished blocks until at least one node in the cluster
// reports a LastPublished timestamp for `keyword` (i.e. one
// full publishOne cycle completed), or budget elapses.
func waitPublished(c *testlab.Cluster, keyword string, budget time.Duration) error {
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		for _, n := range c.Nodes {
			pub := n.Eng.Publisher()
			if pub == nil {
				continue
			}
			for _, ks := range pub.Status().LastPublishes {
				if ks.Keyword == keyword && !ks.LastPublished.IsZero() {
					return nil
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return context.DeadlineExceeded
}
