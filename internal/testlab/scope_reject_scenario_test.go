package testlab_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/swarmsearch"
	"github.com/swartznet/swartznet/internal/testlab"
)

// TestScenarioScopeRejectC0OverWire closes the wire-compat matrix
// row 8.4-B end-to-end: a MiniPeer (acting as a C1 querier) sends
// an sn_search query with scope "c" to a SwartzNet engine configured
// as a C0 responder (ContentHits=0). The engine MUST reply with a
// Reject frame carrying code 2 (RejectUnsupportedScope).
//
// This is the full over-the-wire test that complements the unit-level
// TestHandleQueryScopeRejectC0 in internal/swarmsearch/scope_reject_test.go.
// The unit test verifies the dispatch logic in isolation; this test
// proves the logic survives the full BT handshake → LTEP handshake →
// extension message → reply pipeline.
//
// The test also verifies that the engine's hit cache is not polluted
// by the rejected query: after a reject the hit cache entry count
// for the queried scope must not grow (the reject path returns before
// the local search runs, so no results are produced and nothing is
// cached).
func TestScenarioScopeRejectC0OverWire(t *testing.T) {
	t.Parallel()

	c := testlab.NewCluster(t, 1)
	node := c.Nodes[0]

	// Seed the node's index so there WOULD be something to return
	// if the scope were supported.
	node.IndexTorrent(t, 0x42, "linux kernel source archive")

	// Configure the engine as a C0 node: serves only name/filelist
	// queries, not content-level queries.
	c0Caps := swarmsearch.Capabilities{
		ShareLocal:  2, // answer any query
		FileHits:    1, // can serve file-list hits
		ContentHits: 0, // CANNOT serve content hits (C0)
		Publisher:   0,
	}
	node.Eng.SwarmSearch().SetCapabilities(c0Caps)

	// Snapshot the hit-cache size before the test so we can assert
	// it did not grow after the reject.
	cacheBeforeSize := node.Eng.SwarmSearch().HitCache().Size()

	addr := fmt.Sprintf("127.0.0.1:%d", node.Eng.LocalPort())
	mp, err := testlab.DialMiniPeer(addr, c.SharedInfoHash())
	if err != nil {
		c.DumpLogs(t)
		t.Fatalf("DialMiniPeer: %v", err)
	}
	defer mp.Close()

	snID := mp.RemoteSnSearchID()
	if snID == 0 {
		c.DumpLogs(t)
		t.Fatal("engine did not advertise sn_search; cannot run scope-reject scenario")
	}

	// Send a query with scope "c" — the C1 querier asking the C0
	// responder for content-level hits.
	payload, err := swarmsearch.EncodeQuery(swarmsearch.Query{
		TxID:  77,
		Q:     "linux kernel",
		Scope: "c",
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mp.SendExtended(snID, payload); err != nil {
		t.Fatalf("send query: %v", err)
	}

	// Read responses until we get a Reject or a Result, skipping
	// PeerAnnounce (msg_type=3) which the engine sends on handshake.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, resp, err := mp.RecvExtendedPayload(time.Until(deadline))
		if err != nil {
			c.DumpLogs(t)
			t.Fatalf("recv response: %v", err)
		}

		// Skip PeerAnnounce — it arrives right after the handshake.
		if pa, err := swarmsearch.DecodePeerAnnounce(resp); err == nil {
			t.Logf("skipping PeerAnnounce v=%d", pa.Version)
			continue
		}

		// Must be a Reject with code 2.
		rj, err := swarmsearch.DecodeReject(resp)
		if err != nil {
			// Not a Reject — could be a Result, which is also wrong here.
			if res, err2 := swarmsearch.DecodeResult(resp); err2 == nil {
				t.Fatalf("C0 engine returned a Result for scope=\"c\" query; "+
					"expected RejectUnsupportedScope. hits=%d total=%d",
					len(res.Hits), res.Total)
			}
			t.Fatalf("response was neither Reject nor Result; decode err: %v", err)
		}

		// Correct message type — now check the fields.
		if rj.TxID != 77 {
			t.Errorf("Reject.TxID = %d, want 77", rj.TxID)
		}
		if rj.Code != swarmsearch.RejectUnsupportedScope {
			t.Errorf("Reject.Code = %d, want RejectUnsupportedScope (%d)",
				rj.Code, swarmsearch.RejectUnsupportedScope)
		}
		t.Logf("got expected Reject: code=%d reason=%q", rj.Code, rj.Reason)

		// Verify hit cache was NOT polluted. The reject fires before
		// the local search runs, so the cache must not grow.
		cacheAfterSize := node.Eng.SwarmSearch().HitCache().Size()
		if cacheAfterSize > cacheBeforeSize {
			t.Errorf("hit cache grew from %d to %d after scope reject — "+
				"local search must not run for unsupported scopes",
				cacheBeforeSize, cacheAfterSize)
		}
		return
	}
	c.DumpLogs(t)
	t.Fatal("no Reject or Result received within 3s")
}
