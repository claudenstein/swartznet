package testlab_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/daemon"
	"github.com/swartznet/swartznet/internal/dhtindex"
	"github.com/swartznet/swartznet/internal/swarmsearch"
	"github.com/swartznet/swartznet/internal/testlab"
)

// publisherObserverAdapter is the local copy of the daemon
// adapter — kept here so the scenario is self-contained even
// when internal daemon adapters evolve.
type publisherObserverAdapter struct{ boot *daemon.Bootstrap }

func (a publisherObserverAdapter) NotePublisherSeen(pubkey [32]byte) {
	a.boot.CandidateFromCrawl(pubkey, true)
}

// TestScenarioPublisherObservedViaSync validates the full
// sync → observer → Bootstrap-admission pipeline end to end:
//
//  1. Node A mints Aggregate records under its identity.
//  2. Node B has a manually-wired Bootstrap with no anchors,
//     no bloom, no tracker — a cold subscriber.
//  3. B syncs from A; A's records land in B's cache.
//  4. On every ingested record, the attached
//     PublisherObserver forwards A's pubkey to B's
//     Bootstrap via CandidateFromCrawl.
//  5. Without a bloom/tracker signal the pubkey stays
//     pending (not auto-admitted) — Bootstrap's admission
//     policy correctly gates on stronger signals.
//  6. The scenario asserts A's pubkey is IsPending on B —
//     the admission-path plumbing is alive, the policy just
//     hasn't cleared the gate yet.
//
// Why Bootstrap is constructed here rather than relied on via
// daemon.New: testlab runs with DisableDHT=true so
// eng.Lookup() is nil, which triggers daemon.New's "no
// Bootstrap" path. Manual wiring lets us exercise the sync →
// observer → Bootstrap pipeline in testlab's DHT-free sandbox
// without plumbing a real mainline DHT into the tests.
func TestScenarioPublisherObservedViaSync(t *testing.T) {
	c := testlab.NewCluster(t, 2)
	c.WireMesh(t)
	c.WaitAllHandshaked(t, 10*time.Second)
	time.Sleep(500 * time.Millisecond)

	nodeA, nodeB := c.Nodes[0], c.Nodes[1]

	// Mint records on A.
	var ih [20]byte
	ih[0] = 0xAA
	nodeA.Eng.MintAggregateRecords(ih, "ubuntu debian fedora")
	aRecs := nodeA.Eng.RecordCache().Snapshot()
	if len(aRecs) == 0 {
		t.Fatal("node A minted zero records")
	}
	aPub := aRecs[0].Pk
	t.Logf("node A minted %d records under pubkey %x", len(aRecs), aPub[:4])

	// ----- Wire a minimal Bootstrap into node B -----
	//
	// We need a Lookup even though B has no DHT. Construct one
	// over an in-memory store; it won't be populated with any
	// legacy items, but it's enough for Bootstrap's AddIndexer
	// call on admission.
	mem := dhtindex.NewMemoryPutterGetter(nil)
	lookup := dhtindex.NewLookup(mem)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	opts := daemon.DefaultBootstrapOptions()
	opts.MaxTrackedPublishers = 100
	boot, err := daemon.NewBootstrap(lookup, nil, nil, nil, opts, log)
	if err != nil {
		t.Fatalf("NewBootstrap: %v", err)
	}

	// Attach the adapter so B's sync-record ingestion talks to
	// its Bootstrap.
	nodeB.Eng.SwarmSearch().SetPublisherObserver(publisherObserverAdapter{boot: boot})

	// Verify initial state.
	if boot.IsAdmitted(aPub) {
		t.Fatal("pubkey A must not be admitted on a fresh bootstrap")
	}
	if boot.IsPending(aPub) {
		t.Fatal("pubkey A must not be pending on a fresh bootstrap")
	}

	// ----- Drive sync: B initiates, A serves records -----
	var peerAddr string
	for _, ps := range nodeB.Eng.SwarmSearch().KnownPeers() {
		if ps.Supported && ps.Services.Has(swarmsearch.BitSetReconciliation) {
			peerAddr = ps.Addr
			break
		}
	}
	if peerAddr == "" {
		c.DumpLogs(t)
		t.Fatal("no capable peer for B")
	}

	sess, err := nodeB.Eng.SwarmSearch().StartSync(peerAddr, swarmsearch.SyncFilter{}, nil)
	if err != nil {
		t.Fatalf("StartSync: %v", err)
	}

	// Wait for ID decode.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(sess.NeedIDs()) >= len(aRecs) {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	// Fetch records so ingestSyncRecords fires observer.
	needs := sess.NeedIDs()
	if err := nodeB.Eng.SwarmSearch().SendSyncNeed(peerAddr, sess, needs); err != nil {
		t.Fatal(err)
	}

	// Wait for records to land in B's cache (observer fires
	// in the same code path).
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if nodeB.Eng.RecordCache().Len() >= len(aRecs) {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if nodeB.Eng.RecordCache().Len() == 0 {
		c.DumpLogs(t)
		t.Fatal("B cache still empty — record delivery never happened")
	}

	// ----- Assert: A's pubkey must now be known to B's Bootstrap -----
	// With no bloom + no tracker, bloomPolicy defaults to false,
	// so the candidate stays pending. That's still evidence the
	// observer → Bootstrap pipe is live.
	if !boot.IsPending(aPub) && !boot.IsAdmitted(aPub) {
		c.DumpLogs(t)
		t.Fatalf("A's pubkey %x neither pending nor admitted after sync — observer pipeline broken",
			aPub[:4])
	}
	t.Logf("A's pubkey observed by B's Bootstrap (pending=%v admitted=%v)",
		boot.IsPending(aPub), boot.IsAdmitted(aPub))

	_ = nodeB.Eng.SwarmSearch().CloseSync(peerAddr, sess, swarmsearch.SyncStatusConverged)
}
