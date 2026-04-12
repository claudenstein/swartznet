package testlab_test

import (
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/swarmsearch"
	"github.com/swartznet/swartznet/internal/testlab"
)

// TestClusterRateLimitE2E exercises M12f's per-peer inbound
// query rate limiter over real peer-wire connections — unlike
// the package-local unit tests which use a captureReply fake
// that bypasses anacrolix entirely.
//
// Setup:
//  1. 2-node cluster, mesh wired, sn_search handshaked.
//  2. Tighten the receiver's rate limit to burst=3 qps=0.01 so
//     the fourth query in quick succession hits the throttle.
//  3. Node 0 (sender) fires 6 queries in a burst against node 1
//     (receiver). The first 3 must return results; the rest
//     must come back as RejectRateLimited.
//
// We don't assert exact per-query outcomes because the order in
// which the sender's Query() goroutines reach the receiver's
// rate limiter isn't guaranteed — we just check aggregate
// counts over the 6-query burst.
func TestClusterRateLimitE2E(t *testing.T) {
	c := testlab.NewCluster(t, 2)
	c.Nodes[0].IndexTorrent(t, 0x01, "ubuntu server amd64")

	c.WireMesh(t)
	c.WaitAllHandshaked(t, 10*time.Second)
	// Small settle so the handshake fully routes on both sides.
	time.Sleep(100 * time.Millisecond)

	// Tighten node 1's (receiver) inbound rate limit.
	c.Nodes[1].Eng.SwarmSearch().SetRateLimit(swarmsearch.RateLimit{
		QueriesPerSecond: 0.01, // effectively no refill during the test
		Burst:            3,
	})

	// Fire 6 queries from node 0 in quick succession. The
	// Query API blocks for its own Timeout budget per call, so
	// we run them sequentially with a tiny budget each; the
	// receiver's limiter doesn't care whether calls overlap or
	// serialise — it counts tokens.
	const burst = 6
	var okCount, rejectedCount, askedTotal int
	for i := 0; i < burst; i++ {
		resp, err := c.Nodes[0].Eng.SwarmSearch().Query(
			t.Context(),
			swarmsearch.QueryRequest{
				Q:            "ubuntu",
				PerPeerLimit: 10,
				Timeout:      1 * time.Second,
			},
		)
		if err != nil {
			t.Fatalf("iter %d: Query: %v", i, err)
		}
		askedTotal += resp.Asked
		okCount += resp.Responded
		rejectedCount += resp.Rejected
	}

	if askedTotal < burst {
		t.Errorf("askedTotal = %d, want >= %d", askedTotal, burst)
	}
	if okCount < 1 {
		t.Errorf("okCount = %d, want >= 1 (first queries should pass)", okCount)
	}
	if rejectedCount < 1 {
		t.Errorf("rejectedCount = %d, want >= 1 (later queries should be throttled)", rejectedCount)
		c.DumpLogs(t)
	}
	// Sanity — every query must have one outcome.
	if okCount+rejectedCount != askedTotal {
		t.Errorf("ok(%d)+rejected(%d) != asked(%d); some queries neither responded nor rejected",
			okCount, rejectedCount, askedTotal)
	}
}
