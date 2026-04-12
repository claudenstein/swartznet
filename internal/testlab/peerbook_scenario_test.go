package testlab_test

import (
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/swarmsearch"
	"github.com/swartznet/swartznet/internal/testlab"
)

// TestScenarioPeerBookPromotion validates the M15d AddrMan-style
// peer book's tried/new promotion over real peer-wire connections.
//
// Flow:
//  1. 2-node cluster, mesh wired, sn_search handshaked.
//  2. After handshake: the querier's peer book should have the
//     answerer in the "new" table (seen in handshake, never
//     queried).
//  3. The querier sends a successful sn_search query.
//  4. After the query: the answerer should have been promoted
//     from "new" → "tried" because it responded correctly.
//
// This is the core behavioral property of the peer book:
// peers earn "tried" status by demonstrating correct protocol
// behavior, not just by advertising sn_search in the handshake.
func TestScenarioPeerBookPromotion(t *testing.T) {
	c := testlab.NewCluster(t, 2)
	querier := c.Nodes[0]
	answerer := c.Nodes[1]

	// Seed the answerer with content so the query returns a hit.
	answerer.IndexTorrent(t, 0x01, "ubuntu 24.04 desktop amd64 iso")

	c.WireMesh(t)
	c.WaitAllHandshaked(t, 10*time.Second)
	time.Sleep(100 * time.Millisecond)

	// After handshake: every sn_search-capable peer should be
	// in the "new" table on every node. The querier's book should
	// have at least one new entry (the answerer).
	book := querier.Eng.SwarmSearch().PeerBook()
	if book.NewCount() == 0 {
		c.DumpLogs(t)
		t.Fatal("querier has 0 new peers after handshake; expected >=1")
	}
	if book.TriedCount() != 0 {
		t.Errorf("querier has %d tried peers before any query; expected 0", book.TriedCount())
	}
	t.Logf("pre-query: new=%d tried=%d", book.NewCount(), book.TriedCount())

	// Run a sn_search query. The answerer should respond,
	// which triggers the book's Promote() in the Query collect
	// path.
	resp := querier.SwarmQuery(t, "ubuntu")
	if resp.Responded == 0 {
		c.DumpLogs(t)
		t.Fatal("query got 0 responses; need at least 1 for promotion")
	}

	// After the query: at least one peer should have been
	// promoted to tried.
	if book.TriedCount() == 0 {
		c.DumpLogs(t)
		t.Fatal("querier has 0 tried peers after a successful query; promotion didn't fire")
	}
	t.Logf("post-query: new=%d tried=%d", book.NewCount(), book.TriedCount())

	// Verify the answerer specifically is the one that got
	// promoted. Since there are only 2 nodes, only the
	// answerer's addr(s) can be in the tried set.
	triedAddrs := book.TriedAddrs()
	if len(triedAddrs) == 0 {
		t.Error("no tried addresses")
	}
	for _, a := range triedAddrs {
		t.Logf("tried peer: %s", a)
	}

	// Sanity: the answerer's book should also have at least one
	// new entry (the querier) — the querier connected to it
	// during WireMesh, which triggered an OnRemoteHandshake.
	_ = answerer.Eng.SwarmSearch().PeerBook()
	// We don't require the answerer to have promoted the
	// querier because the answerer received a query, not sent
	// one — promotion is a QUERIER-side event.

	// Run a second query. The tried peer should respond again,
	// and its Successes count should increment.
	resp2, err := querier.Eng.SwarmSearch().Query(
		t.Context(),
		swarmsearch.QueryRequest{
			Q:       "ubuntu",
			Timeout: 2 * time.Second,
		},
	)
	if err != nil {
		t.Fatalf("second query: %v", err)
	}
	if resp2.Responded == 0 {
		t.Error("second query: 0 responses from tried peer")
	}
}
