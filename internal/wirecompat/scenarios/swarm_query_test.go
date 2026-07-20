package scenarios

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/swartznet/swartznet/contracts/ltepwire"
	"github.com/swartznet/swartznet/internal/indexer"
	"github.com/swartznet/swartznet/internal/wirecompat"
)

// TestSwarmQueryOverRealWire is the Slice 7 DoD scenario: a peer (a raw-socket
// MiniPeer that negotiated sn_search) queries the engine over the real LTEP
// wire and gets the engine's indexed hits back. It exercises the whole inbound
// path — LTEP dispatch → HandleMessage → LocalSearcher → EncodeResult → reply.
func TestSwarmQueryOverRealWire(t *testing.T) {
	if testing.Short() {
		t.Skip("raw-socket wire query; run without -short")
	}
	c := wirecompat.NewCluster(t, 1)
	eng := c.Nodes[0].Eng
	// The cluster's bare config defaults ShareLocal to 0 (off); enable full
	// local sharing so the engine answers queries.
	eng.SetSharing(ltepwire.Sharing{ShareLocal: 2, FileHits: true, ContentHits: true})

	// The engine indexes a torrent whose content carries a unique marker.
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "index"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	eng.SetIndex(idx)

	content := filepath.Join(c.Nodes[0].DataDir, "content")
	body := []byte("the archive preserves knowledge. swarmmarker unique token here.\n")
	mi, _ := wirecompat.BuildFixtureBytes(t, content, "book.txt", body)
	if _, err := eng.AddTorrentMetaInfoSeedFrom(mi, filepath.Join(content, "book.txt")); err != nil {
		t.Fatal(err)
	}
	ih := mi.HashInfoBytes()

	waitFor(t, 15*time.Second, "engine indexes the marker", func() bool {
		resp, err := idx.Search(indexer.SearchRequest{Query: "swarmmarker", Limit: 5})
		return err == nil && resp.Total > 0
	})

	// A capable MiniPeer connects and queries over the real wire.
	addr := fmt.Sprintf("127.0.0.1:%d", eng.LocalPort())
	mp, err := wirecompat.DialMiniPeer(addr, [20]byte(ih))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer mp.Close()
	snID := mp.RemoteSnSearchID()
	if snID == 0 {
		t.Fatal("engine did not advertise sn_search")
	}

	// Send the query under the id the engine advertised for sn_search.
	q, _ := ltepwire.EncodeQuery(ltepwire.Query{TxID: 7, Q: "swarmmarker"})
	if err := mp.SendExtended(snID, q); err != nil {
		t.Fatal(err)
	}

	// Read frames until we get the RESULT (the peer_announce may arrive first).
	deadline := time.Now().Add(5 * time.Second)
	var result *ltepwire.Result
	for time.Now().Before(deadline) && result == nil {
		payload, err := mp.RecvSnSearchPayload(time.Until(deadline))
		if err != nil {
			break
		}
		mt, err := ltepwire.PeekMsgType(payload)
		if err != nil || mt != ltepwire.MsgTypeResult {
			continue
		}
		r, err := ltepwire.DecodeResult(payload)
		if err != nil {
			t.Fatalf("decode result: %v", err)
		}
		result = &r
	}
	if result == nil {
		t.Fatal("no sn_search result frame received over the wire")
	}
	if result.TxID != 7 {
		t.Errorf("result txid = %d, want 7 (echoed)", result.TxID)
	}
	if len(result.Hits) == 0 {
		t.Fatalf("engine returned no hits for the marker: %+v", result)
	}
	if fmt.Sprintf("%x", result.Hits[0].IH) != ih.HexString() {
		t.Errorf("hit infohash = %x, want %s", result.Hits[0].IH, ih.HexString())
	}
}
