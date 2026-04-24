package testlab_test

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/dhtindex"
	"github.com/swartznet/swartznet/internal/testlab"
)

// TestLayerDPublisherRefreshKeepsItemFresh verifies that the
// dhtindex.Publisher's refresh ticker actually re-publishes
// stored items, which is what keeps BEP-44 items alive past
// their 2h expiry in production. Under regtest, the refresh
// interval is 5s (see dhtindex.RegtestPublisherOptions), so
// after waiting two full refresh cycles we expect the
// PublisherStatus's LastPublished timestamp for the keyword
// to have advanced.
//
// Why this matters: the Publisher.refreshAll code path is
// separate from the Submit → handleTask → publishOne path
// exercised by TestLayerDDHTClusterRoundTrip. If refreshAll
// ever stops firing (a tick-loop bug, a cancelled context, a
// silently-swallowed error in publishOne), items would
// eventually expire from the DHT and Layer-D lookups would
// return stale misses. This test catches that regression
// within the regtest budget.
func TestLayerDPublisherRefreshKeepsItemFresh(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping publisher refresh cycle in -short mode")
	}

	// 2 seeds + 1 leech is plenty — we're only asserting that
	// the refresh ticker re-fires, not the merge path.
	const total = 3
	c := testlab.NewDHTCluster(t, total)
	c.WireMesh(t)
	c.WaitAllHandshaked(t, 15*time.Second)
	time.Sleep(1 * time.Second)

	// Seed 0 is the publisher we monitor. Seed 1 exists so
	// the put traversal has somewhere to store.
	pub := c.Nodes[0].Eng.Publisher()
	if pub == nil {
		t.Fatalf("seed 0: Engine.Publisher() is nil")
	}

	keyword := "refreshcorpus"
	var ih [20]byte
	for i := range ih {
		ih[i] = 0xD0
	}
	pub.Submit(dhtindex.PublishTask{
		InfoHash: ih[:],
		Name:     keyword,
		Seeders:  1,
	})

	// Wait for the first put to complete.
	firstPublished, err := waitForPublishedTimestamp(pub, keyword, 15*time.Second)
	if err != nil {
		t.Fatalf("no first publish for %q: %v", keyword, err)
	}
	t.Logf("first publish at %v", firstPublished)

	// Regtest refresh interval is 5 s. Wait past one full cycle
	// (up to 12 s) and poll for a strictly-newer LastPublished
	// timestamp. If the ticker stopped or the refresh path
	// silently errored, this never advances.
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		latest := publisherLastPublished(pub, keyword)
		if latest.After(firstPublished) {
			t.Logf("refresh advanced LastPublished: %v → %v (delta %v)",
				firstPublished, latest, latest.Sub(firstPublished))

			// Sanity: the leech's Lookup can still resolve the
			// keyword post-refresh (the re-put didn't corrupt
			// anything).
			leech := c.Nodes[total-1]
			look := leech.Eng.Lookup()
			look.AddIndexer(c.Nodes[0].Eng.Identity().PublicKeyBytes(), "seed")
			qctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			resp, qerr := look.Query(qctx, keyword)
			cancel()
			if qerr != nil {
				t.Fatalf("post-refresh Lookup.Query: %v", qerr)
			}
			if resp.IndexersResponded < 1 || len(resp.Hits) < 1 {
				t.Fatalf("post-refresh lookup empty: asked=%d responded=%d hits=%d",
					resp.IndexersAsked, resp.IndexersResponded, len(resp.Hits))
			}
			found := false
			wantIH := hex.EncodeToString(ih[:])
			for _, h := range resp.Hits {
				if h.InfoHash == wantIH {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("post-refresh hits did not contain %s", wantIH)
			}
			return
		}
		time.Sleep(500 * time.Millisecond)
	}

	c.DumpLogs(t)
	t.Fatalf("publisher.LastPublished never advanced past %v within 12 s — "+
		"refresh ticker may have stopped firing", firstPublished)
}

// waitForPublishedTimestamp blocks until the publisher's status
// reports a non-zero LastPublished for the keyword, returning
// that timestamp. Returns context.DeadlineExceeded on timeout.
func waitForPublishedTimestamp(pub *dhtindex.Publisher, keyword string, budget time.Duration) (time.Time, error) {
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if ts := publisherLastPublished(pub, keyword); !ts.IsZero() {
			return ts, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return time.Time{}, context.DeadlineExceeded
}

// publisherLastPublished returns the LastPublished timestamp
// recorded in the Publisher's status for the keyword, or the
// zero Time if no record exists.
func publisherLastPublished(pub *dhtindex.Publisher, keyword string) time.Time {
	for _, ks := range pub.Status().LastPublishes {
		if ks.Keyword == keyword {
			return ks.LastPublished
		}
	}
	return time.Time{}
}
