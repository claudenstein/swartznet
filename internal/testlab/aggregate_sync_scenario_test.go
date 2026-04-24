package testlab_test

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/swarmsearch"
	"github.com/swartznet/swartznet/internal/testlab"
)

// TestScenarioAggregateSyncRoundTrip exercises the full Aggregate
// sync flow between two real engines:
//
//   1. Node A mints 20 Aggregate records (one per keyword-token)
//      into its RecordCache.
//   2. Both nodes LTEP-handshake via the cluster's WireMesh.
//   3. Node B calls Protocol.StartSync against node A's address.
//   4. Node A's handler responds with sync_symbols carrying
//      RIBLT symbols encoded over A's record-ID set.
//   5. Node B's decoder converges and NeedIDs returns the IDs A
//      has that B lacks.
//   6. Assert the decoded need-set matches the records A minted.
//
// This is the first integration test where sync frames actually
// traverse the LTEP transport — every earlier sync test ran
// in-process via direct HandleMessage calls.
func TestScenarioAggregateSyncRoundTrip(t *testing.T) {
	c := testlab.NewCluster(t, 2)
	c.WireMesh(t)
	c.WaitAllHandshaked(t, 10*time.Second)

	// PeerAnnounce is fire-and-forget; give the handshake's
	// goroutine-driven announce flow a moment to settle so
	// KnownPeers().Services reliably carries bit 9.
	time.Sleep(500 * time.Millisecond)

	nodeA, nodeB := c.Nodes[0], c.Nodes[1]

	// ----- Step 1: mint records on A -----
	// MintAggregateRecords uses A's identity key which the
	// engine loaded from its per-node cfg.IdentityPath.
	const kws = "ubuntu debian fedora linux kernel"
	var ih [20]byte
	ih[0] = 0xAA
	nodeA.Eng.MintAggregateRecords(ih, kws)

	aCache := nodeA.Eng.RecordCache()
	if aCache.Len() == 0 {
		t.Fatalf("node A cache empty after Mint; identity missing?")
	}
	t.Logf("node A minted %d records", aCache.Len())

	// Build the set of IDs A has. B should end up asking for
	// every one of these since its cache is empty.
	aRecords := aCache.Snapshot()
	wantIDs := make(map[[32]byte]bool, len(aRecords))
	for _, r := range aRecords {
		wantIDs[recordID(r)] = true
	}

	// ----- Step 2: B finds A's peer address -----
	var peerAddrFromB string
	for _, ps := range nodeB.Eng.SwarmSearch().KnownPeers() {
		if ps.Supported && ps.Services.Has(swarmsearch.BitSetReconciliation) {
			peerAddrFromB = ps.Addr
			break
		}
	}
	if peerAddrFromB == "" {
		c.DumpLogs(t)
		t.Fatal("node B found no capable peer to sync from")
	}
	t.Logf("node B syncing against %s", peerAddrFromB)

	// ----- Step 3: B initiates sync -----
	sess, err := nodeB.Eng.SwarmSearch().StartSync(
		peerAddrFromB,
		swarmsearch.SyncFilter{},
		nil, // node B's own records: empty
	)
	if err != nil {
		t.Fatalf("StartSync: %v", err)
	}

	// ----- Step 4: wait for B's decoder to converge -----
	// Convergence here means A's symbols are enough for B to
	// decode the full set of "A has these, I don't" IDs. Because
	// B's record set is empty and A has ~5 records, the diff is
	// tiny — a single sync_symbols batch from A should suffice.
	converged := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		needs := sess.NeedIDs()
		if len(needs) >= len(wantIDs) {
			converged = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	needs := sess.NeedIDs()
	if !converged {
		c.DumpLogs(t)
		t.Fatalf("node B decoded %d IDs, want %d", len(needs), len(wantIDs))
	}
	t.Logf("node B decoded %d IDs (want %d)", len(needs), len(wantIDs))

	// ----- Step 5: verify B's decoded IDs match A's records -----
	for _, id := range needs {
		if !wantIDs[id] {
			t.Errorf("B decoded unexpected ID %x — not in A's cache", id[:4])
		}
	}
	// Some decodings can also land in RemovedIDs if there's any
	// overlap; assert the intended direction dominates.
	if len(sess.RemovedIDs()) > 0 {
		t.Errorf("unexpected RemovedIDs count = %d (B has no records to be 'removed')",
			len(sess.RemovedIDs()))
	}

	// ----- Step 6: B pulls the actual record bytes -----
	// SendSyncNeed asks A for the records behind each decoded
	// ID. A's handler looks them up in its session's
	// pre-indexed cache (populated at NewSyncSession time from
	// A's RecordSource) and replies with sync_records. B's
	// handler then verifies each sig and adds to B's RecordSink
	// — the same *RecordCache B reads from for future sync
	// sessions.
	bCache := nodeB.Eng.RecordCache()
	if bCache.Len() != 0 {
		t.Fatalf("node B cache should be empty before sync_need, got %d", bCache.Len())
	}

	if err := nodeB.Eng.SwarmSearch().SendSyncNeed(peerAddrFromB, sess, needs); err != nil {
		t.Fatalf("SendSyncNeed: %v", err)
	}

	// Wait for A's sync_records reply to land in B's cache.
	gotRecords := false
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if bCache.Len() >= len(wantIDs) {
			gotRecords = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !gotRecords {
		c.DumpLogs(t)
		t.Fatalf("node B cache has %d records after SendSyncNeed, want %d",
			bCache.Len(), len(wantIDs))
	}
	t.Logf("node B ingested %d records from peer A", bCache.Len())

	// Verify every record in B's cache is one A minted.
	for _, r := range bCache.Snapshot() {
		if !wantIDs[recordID(r)] {
			t.Errorf("B ingested unknown record: kw=%q ih=%x", r.Kw, r.Ih[:4])
		}
	}

	// Clean close of the session.
	if err := nodeB.Eng.SwarmSearch().CloseSync(peerAddrFromB, sess, swarmsearch.SyncStatusConverged); err != nil {
		t.Logf("CloseSync: %v", err)
	}
}

// recordID mirrors the unexported swarmsearch localRecordID /
// cacheRecordID helpers so this test can derive the same key
// the sync protocol uses. SHA-256 over pk || kw || ih || t_LE,
// bit-for-bit identical to the swarmsearch internals.
func recordID(r swarmsearch.LocalRecord) [32]byte {
	msg := make([]byte, 0, 32+len(r.Kw)+20+8)
	msg = append(msg, r.Pk[:]...)
	msg = append(msg, r.Kw...)
	msg = append(msg, r.Ih[:]...)
	var ts [8]byte
	for i := 0; i < 8; i++ {
		ts[i] = byte(r.T >> (8 * i))
	}
	msg = append(msg, ts[:]...)
	return sha256.Sum256(msg)
}
