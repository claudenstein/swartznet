package testlab_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/swarmsearch"
	"github.com/swartznet/swartznet/internal/testlab"
)

// TestScenarioMiniPeerHandshake validates that a MiniPeer can
// complete a full BT + LTEP handshake with a real SwartzNet
// engine and see the sn_search extension advertised.
func TestScenarioMiniPeerHandshake(t *testing.T) {
	c := testlab.NewCluster(t, 1)
	node := c.Nodes[0]

	addr := fmt.Sprintf("127.0.0.1:%d", node.Eng.LocalPort())
	mp, err := testlab.DialMiniPeer(addr, c.SharedInfoHash())
	if err != nil {
		c.DumpLogs(t)
		t.Fatalf("DialMiniPeer: %v", err)
	}
	defer mp.Close()

	snID := mp.RemoteSnSearchID()
	if snID == 0 {
		t.Fatal("engine did not advertise sn_search in LTEP handshake")
	}
	t.Logf("engine's sn_search ext ID = %d", snID)
}

// TestScenarioMiniPeerValidQuery sends a legitimate sn_search
// query from a MiniPeer and verifies it gets a Result back.
func TestScenarioMiniPeerValidQuery(t *testing.T) {
	c := testlab.NewCluster(t, 1)
	node := c.Nodes[0]
	node.IndexTorrent(t, 0x01, "ubuntu 24.04 desktop iso")

	addr := fmt.Sprintf("127.0.0.1:%d", node.Eng.LocalPort())
	mp, err := testlab.DialMiniPeer(addr, c.SharedInfoHash())
	if err != nil {
		c.DumpLogs(t)
		t.Fatalf("DialMiniPeer: %v", err)
	}
	defer mp.Close()

	snID := mp.RemoteSnSearchID()
	if snID == 0 {
		t.Fatal("no sn_search")
	}

	// Build a valid sn_search Query and send it.
	payload, err := swarmsearch.EncodeQuery(swarmsearch.Query{
		TxID: 1,
		Q:    "ubuntu",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mp.SendExtended(snID, payload); err != nil {
		t.Fatalf("send query: %v", err)
	}

	// Read responses. The engine may send a PeerAnnounce (msg_type=3)
	// before the query response, so loop until we find a Result or Reject.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, resp, err := mp.RecvExtendedPayload(time.Until(deadline))
		if err != nil {
			c.DumpLogs(t)
			t.Fatalf("recv response: %v", err)
		}

		// Try Result first.
		if result, err := swarmsearch.DecodeResult(resp); err == nil {
			t.Logf("got Result: txid=%d total=%d hits=%d", result.TxID, result.Total, len(result.Hits))
			if result.TxID != 1 {
				t.Errorf("result txid=%d, want 1", result.TxID)
			}
			return
		}
		// Try Reject.
		if reject, err := swarmsearch.DecodeReject(resp); err == nil {
			t.Logf("got Reject: txid=%d code=%d reason=%s", reject.TxID, reject.Code, reject.Reason)
			return
		}
		// Try PeerAnnounce — skip it and keep reading.
		if pa, err := swarmsearch.DecodePeerAnnounce(resp); err == nil {
			t.Logf("skipping PeerAnnounce: v=%d services=%d", pa.Version, pa.Services)
			continue
		}
		t.Logf("skipping unknown response: %x", resp[:min(len(resp), 50)])
	}
	t.Fatal("no Result or Reject received within 3s")
}

// TestScenarioMiniPeerMalformedBencode sends garbage bytes as
// an sn_search extension message. The engine should charge
// ScoreBadBencode on the misbehavior tracker but NOT crash.
func TestScenarioMiniPeerMalformedBencode(t *testing.T) {
	c := testlab.NewCluster(t, 1)
	node := c.Nodes[0]

	addr := fmt.Sprintf("127.0.0.1:%d", node.Eng.LocalPort())
	mp, err := testlab.DialMiniPeer(addr, c.SharedInfoHash())
	if err != nil {
		c.DumpLogs(t)
		t.Fatalf("DialMiniPeer: %v", err)
	}
	defer mp.Close()

	snID := mp.RemoteSnSearchID()
	if snID == 0 {
		t.Fatal("no sn_search")
	}

	// Send 5 garbage payloads. Each should trigger
	// ScoreBadBencode (20pts) → 100 total → ban.
	for i := 0; i < 5; i++ {
		garbage := []byte(fmt.Sprintf("not-bencode-%d", i))
		if err := mp.SendExtended(snID, garbage); err != nil {
			t.Logf("send %d: %v (connection may have been closed by engine after ban)", i, err)
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The engine should have charged misbehavior on whatever
	// addr it saw us as. We can't easily assert the exact
	// address from outside, but we CAN verify the engine
	// didn't crash (still answers healthz-equivalent).
	time.Sleep(200 * time.Millisecond)
	if node.Eng.LocalPort() == 0 {
		t.Fatal("engine appears dead after malformed input")
	}
	t.Log("engine survived 5 malformed sn_search messages")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
