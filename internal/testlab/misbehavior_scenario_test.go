package testlab_test

import (
	"context"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/swarmsearch"
	"github.com/swartznet/swartznet/internal/testlab"
)

// TestScenarioMisbehaviorBan validates M15c's misbehavior
// scoring + local banlist over REAL peer-wire connections.
// Two nodes, attacker fires a burst of 1-character sn_search
// queries at the victim. The victim's handler charges
// ScoreQueryTooBroad (5) per query and ScoreRateLimited (5)
// per throttled query. After enough queries the victim bans
// the attacker and subsequent messages are silently dropped.
//
// Flow:
//  1. 2-node cluster, mesh wired, sn_search handshaked.
//  2. Node 1 (victim) is the receiver. Node 0 (attacker)
//     sends queries via Protocol.Query.
//  3. Loop sending 1-character queries until the victim's
//     misbehavior score for the attacker crosses BanThreshold
//     (100 points).
//  4. Assert: victim reports the attacker as banned.
//  5. Sanity: attacker's next query receives no response
//     (the victim's HandleMessage early-returns on banned
//     peers, so no reject and no result come back).
func TestScenarioMisbehaviorBan(t *testing.T) {
	c := testlab.NewCluster(t, 2)
	attacker := c.Nodes[0]
	victim := c.Nodes[1]

	c.WireMesh(t)
	c.WaitAllHandshaked(t, 10*time.Second)
	time.Sleep(100 * time.Millisecond)

	// Grab the attacker's address as seen by the victim —
	// that's the key the misbehavior tracker uses. The victim's
	// KnownPeers list contains the attacker's *remote* address
	// (what the victim sees when the attacker dials in or gets
	// dialed by the victim). Use whichever one is marked
	// Supported.
	victimPeers := victim.Eng.SwarmSearch().KnownPeers()
	var attackerAddr string
	for _, ps := range victimPeers {
		if ps.Supported {
			attackerAddr = ps.Addr
			break
		}
	}
	if attackerAddr == "" {
		c.DumpLogs(t)
		t.Fatal("victim has no supported peer to use as attacker address")
	}
	t.Logf("attacker address as seen by victim: %s", attackerAddr)

	// Fire up to 40 one-character queries. Each one hits
	// either ScoreQueryTooBroad (5) or ScoreRateLimited (5)
	// depending on whether the rate limiter happens to throttle
	// it. 100 / 5 = 20 queries minimum to cross the ban
	// threshold; use 40 as headroom so the test isn't flaky.
	ctx := context.Background()
	var lastScore int
	for i := 0; i < 40; i++ {
		_, _ = attacker.Eng.SwarmSearch().Query(ctx, swarmsearch.QueryRequest{
			Q:            "x", // 1 char — triggers query-too-broad
			PerPeerLimit: 10,
			Timeout:      500 * time.Millisecond,
		})
		// Between sends, poll the victim's view of the
		// attacker's misbehavior score. Exit the loop early
		// as soon as the victim has banned the attacker.
		score := victim.Eng.SwarmSearch().MisbehaviorScore(attackerAddr)
		lastScore = score
		if victim.Eng.SwarmSearch().IsBanned(attackerAddr) {
			t.Logf("attacker banned after %d queries (score %d)", i+1, score)
			break
		}
		// Small yield so the victim's background handler
		// goroutine can process the previous query and charge
		// its score before the next Query call.
		time.Sleep(20 * time.Millisecond)
	}

	if !victim.Eng.SwarmSearch().IsBanned(attackerAddr) {
		c.DumpLogs(t)
		t.Fatalf("attacker not banned after 40 bad queries; last score %d", lastScore)
	}

	// After the ban, the victim's HandleMessage early-returns
	// on banned peers, so the attacker's next query should
	// time out (no reject, no result). Give it a generous
	// 1-second window because the attacker's Query.Timeout
	// controls when it gives up waiting for responses.
	resp, err := attacker.Eng.SwarmSearch().Query(ctx, swarmsearch.QueryRequest{
		Q:       "ubuntu server amd64",
		Timeout: 500 * time.Millisecond,
	})
	if err == nil && resp != nil && resp.Responded > 0 {
		t.Errorf("banned attacker still got responses: %+v", resp)
	}
	t.Logf("post-ban query: err=%v asked=%d responded=%d rejected=%d",
		err, safeAsked(resp), safeResponded(resp), safeRejected(resp))
}

// safeAsked / safeResponded / safeRejected let the test's
// post-ban log line tolerate a nil response (which happens if
// Query fails with ErrNoCapablePeers because the ban removed
// the attacker's view of the victim as a capable peer).
func safeAsked(r *swarmsearch.QueryResponse) int {
	if r == nil {
		return 0
	}
	return r.Asked
}

func safeResponded(r *swarmsearch.QueryResponse) int {
	if r == nil {
		return 0
	}
	return r.Responded
}

func safeRejected(r *swarmsearch.QueryResponse) int {
	if r == nil {
		return 0
	}
	return r.Rejected
}
