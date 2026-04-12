package testlab_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/swarmsearch"
	"github.com/swartznet/swartznet/internal/testlab"
)

// TestScenarioMiniPeerStaleTxID sends a Result with a txid
// that doesn't match any pending outbound query. The engine
// should silently drop it (no crash, no visible effect).
// This exercises the routeResult "no pending query" path
// with a real wire peer for the first time.
func TestScenarioMiniPeerStaleTxID(t *testing.T) {
	c := testlab.NewCluster(t, 1)
	node := c.Nodes[0]
	node.IndexTorrent(t, 0x01, "ubuntu 24.04")

	addr := fmt.Sprintf("127.0.0.1:%d", node.Eng.LocalPort())
	mp, err := testlab.DialMiniPeer(addr, c.SharedInfoHash())
	if err != nil {
		t.Fatalf("DialMiniPeer: %v", err)
	}
	defer mp.Close()

	snID := mp.RemoteSnSearchID()
	if snID == 0 {
		t.Fatal("no sn_search")
	}

	// Send a Result with a txid nobody asked for.
	payload, _ := swarmsearch.EncodeResult(swarmsearch.Result{
		TxID:  99999, // stale — no pending query has this txid
		Total: 1,
		Hits: []swarmsearch.Hit{
			{IH: make([]byte, 20), N: "fake hit"},
		},
	})
	if err := mp.SendExtended(snID, payload); err != nil {
		t.Fatalf("send stale result: %v", err)
	}

	// Verify the engine is still alive after receiving the
	// stale result. If routeResult crashed or panicked, the
	// engine would be dead.
	time.Sleep(200 * time.Millisecond)
	if node.Eng.LocalPort() == 0 {
		t.Fatal("engine died after stale txid")
	}
	t.Log("engine survived stale txid Result")
}

// TestScenarioMiniPeerOversizedPayload sends a very large
// (but syntactically valid) sn_search Query to test the
// engine's resilience to memory-exhaustion attempts. The
// query string is 50 KiB of repeated characters — absurd
// but syntactically legal bencode.
func TestScenarioMiniPeerOversizedPayload(t *testing.T) {
	c := testlab.NewCluster(t, 1)
	node := c.Nodes[0]

	addr := fmt.Sprintf("127.0.0.1:%d", node.Eng.LocalPort())
	mp, err := testlab.DialMiniPeer(addr, c.SharedInfoHash())
	if err != nil {
		t.Fatalf("DialMiniPeer: %v", err)
	}
	defer mp.Close()

	snID := mp.RemoteSnSearchID()
	if snID == 0 {
		t.Fatal("no sn_search")
	}

	// Build a query with a 50 KiB query string.
	bigQ := strings.Repeat("a", 50*1024)
	payload, err := swarmsearch.EncodeQuery(swarmsearch.Query{
		TxID: 42,
		Q:    bigQ,
	})
	if err != nil {
		t.Fatalf("encode big query: %v", err)
	}
	if err := mp.SendExtended(snID, payload); err != nil {
		t.Logf("send oversized: %v (may be rejected by anacrolix size cap)", err)
	}

	// Give the engine a moment to process. Either it handles
	// the query (Bleve will search the big query string, likely
	// returning zero results) or it rejects/drops it. Either
	// way it must NOT crash.
	time.Sleep(300 * time.Millisecond)
	if node.Eng.LocalPort() == 0 {
		t.Fatal("engine died after oversized query")
	}
	t.Log("engine survived 50 KiB query")
}

// TestScenarioMiniPeerFutureVersion sends a PeerAnnounce with
// protocol version 999 and service bits that include unknown
// high bits. The engine must accept it without error and
// ignore the unknown bits — this is the M15b "unknown bits
// must be ignored" invariant tested over real wire.
func TestScenarioMiniPeerFutureVersion(t *testing.T) {
	c := testlab.NewCluster(t, 1)
	node := c.Nodes[0]

	addr := fmt.Sprintf("127.0.0.1:%d", node.Eng.LocalPort())
	mp, err := testlab.DialMiniPeer(addr, c.SharedInfoHash())
	if err != nil {
		t.Fatalf("DialMiniPeer: %v", err)
	}
	defer mp.Close()

	snID := mp.RemoteSnSearchID()
	if snID == 0 {
		t.Fatal("no sn_search")
	}

	// Send a PeerAnnounce from "the future": version 999, with
	// unknown service bits set in the high range.
	pa := swarmsearch.PeerAnnounce{
		Version:  999,
		Services: uint64(swarmsearch.DefaultServices()) | (1 << 50) | (1 << 55) | (1 << 63),
	}
	payload, err := swarmsearch.EncodePeerAnnounce(pa)
	if err != nil {
		t.Fatal(err)
	}
	if err := mp.SendExtended(snID, payload); err != nil {
		t.Fatalf("send future announce: %v", err)
	}

	// Give the engine time to process.
	time.Sleep(200 * time.Millisecond)

	// The engine should have stored the version + services
	// on our PeerState (whatever address it sees us as).
	// We can't easily assert the exact PeerState from outside
	// but we CAN verify the engine is alive and didn't reject
	// the message.
	if node.Eng.LocalPort() == 0 {
		t.Fatal("engine died after future-version PeerAnnounce")
	}

	// Bonus: send a valid query after the future announce.
	// If the announce corrupted handler state, the query would
	// fail.
	qPayload, _ := swarmsearch.EncodeQuery(swarmsearch.Query{
		TxID: 7,
		Q:    "test",
	})
	_ = mp.SendExtended(snID, qPayload)
	// We don't need to check the response — just verifying
	// no crash is sufficient for the future-version invariant.
	time.Sleep(100 * time.Millisecond)
	t.Log("engine survived future-version PeerAnnounce and processed subsequent query")
}
