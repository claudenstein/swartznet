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

	// Fire a burst of 1-char queries. Each charges
	// ScoreQueryTooBroad(5) on the victim. Because each node
	// may have multiple addresses (IPv4 + IPv6 × listen ports),
	// the score spreads across entries. Send enough queries so
	// at least one address crosses BanThreshold(100) and check
	// ALL known addresses, not just attackerAddr.
	ctx := context.Background()
	const maxQueries = 60
	for i := 0; i < maxQueries; i++ {
		_, _ = attacker.Eng.SwarmSearch().Query(ctx, swarmsearch.QueryRequest{
			Q:            "x",
			PerPeerLimit: 10,
			Timeout:      500 * time.Millisecond,
		})
		if anyAttackerBanned(victim) {
			t.Logf("attacker banned after %d queries", i+1)
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if !anyAttackerBanned(victim) {
		c.DumpLogs(t)
		for _, ps := range victim.Eng.SwarmSearch().KnownPeers() {
			t.Logf("  victim: %s score=%d banned=%v",
				ps.Addr,
				victim.Eng.SwarmSearch().MisbehaviorScore(ps.Addr),
				victim.Eng.SwarmSearch().IsBanned(ps.Addr),
			)
		}
		t.Fatalf("no attacker address banned after %d queries", maxQueries)
	}

	// Post-ban: fewer (or zero) responses because the banned
	// addresses silently drop messages.
	resp, err := attacker.Eng.SwarmSearch().Query(ctx, swarmsearch.QueryRequest{
		Q:       "ubuntu server amd64",
		Timeout: 500 * time.Millisecond,
	})
	t.Logf("post-ban query: err=%v asked=%d responded=%d rejected=%d",
		err, safeAsked(resp), safeResponded(resp), safeRejected(resp))
}

// anyAttackerBanned checks if ANY peer the victim knows is
// banned. In a 2-node cluster, all addresses belong to the
// attacker or the victim itself.
func anyAttackerBanned(victim *testlab.Node) bool {
	for _, ps := range victim.Eng.SwarmSearch().KnownPeers() {
		if victim.Eng.SwarmSearch().IsBanned(ps.Addr) {
			return true
		}
	}
	return false
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
