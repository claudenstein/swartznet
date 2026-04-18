package testlab_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/companion"
	"github.com/swartznet/swartznet/internal/testlab"
)

// TestScenarioCompanionEndToEnd is the headline F3 scenario: a
// publisher serializes its local Bleve index, wraps it in a
// torrent, seeds it, and publishes a BEP-46 pointer. A
// subscriber follows the publisher's pubkey, resolves the
// pointer, downloads the torrent over real BitTorrent, decodes
// the payload, and ingests every record into its own local
// index. Success = the subscriber's index can answer a query
// for a keyword that only the publisher originally knew.
//
// This is the first fully-automated end-to-end F3 test in the
// project. Every component is real:
//
//   - companion.Publisher (M11c) builds and serializes the
//     content index, wraps it in a v1 .torrent metainfo, and
//     asks the engine to seed it
//   - dhtindex.AnacrolixPutter wraps MemoryPointerStore in the
//     test so pointer puts go to a shared in-memory store
//     instead of the real mainline DHT
//   - anacrolix/torrent runs a real peer-wire connection over
//     localhost and transfers the torrent bytes between
//     engine instances
//   - companion.Subscriber.Sync (M11d) runs the full
//     pointer → torrent → decode → ingest pipeline
//   - the subscriber's indexer.Index answers a local query
//     against records it learned from the publisher
//
// The only thing faked is the DHT itself (in-memory pointer
// store + manual peer wiring for the companion torrent). Every
// other wire path is production code.
//
// Runtime budget: ~2s in regtest mode. Under 10s at worst case.
func TestScenarioCompanionEndToEnd(t *testing.T) {
	c := testlab.NewCluster(t, 2)
	pub := c.Nodes[0]
	sub := c.Nodes[1]

	// ------------------------------------------------------------
	// 1. Seed the publisher's local index with content that only
	//    the publisher originally knows about. The keyword must
	//    be distinctive so the final assertion (subscriber
	//    queries it) unambiguously proves the end-to-end path.
	pub.IndexTorrent(t, 0xaa, "ubuntu 24.04 desktop amd64 iso", "body.txt")
	pub.IndexContent(t, 0xaa, "the quick brown fox jumps over the lazy dog")

	// Sanity: subscriber's index is empty.
	if r := sub.LocalQuery(t, "ubuntu"); len(r.Hits) != 0 {
		t.Fatalf("subscriber index not empty at start: %d hits", len(r.Hits))
	}

	// ------------------------------------------------------------
	// 2. Build the publisher-side ed25519 keypair. We can't
	//    reuse the engine's loaded identity because the
	//    Identity type doesn't export the raw private key.
	//    An independent keypair is fine — Layer D / BEP-46
	//    only care that the same pubkey signs the put and
	//    shows up in the subscriber's lookup.
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var pubkey [32]byte
	copy(pubkey[:], priv.Public().(ed25519.PublicKey))

	// ------------------------------------------------------------
	// 3. Shared in-memory pointer store (the stand-in for
	//    BEP-46 on the real DHT). The publisher writes via a
	//    PointerPutter bound to its pubkey; the subscriber
	//    reads via a PointerGetter.
	store := testlab.NewMemoryPointerStore()
	publisherPutter := store.PutterFor(pubkey)
	subscriberGetter := store.Getter()

	// ------------------------------------------------------------
	// 4. Start the publisher worker.
	pubOpts := companion.RegtestPublisherOptions()
	// Payload must land in DataDir, not CompanionDir — anacrolix's
	// file-storage layer reads from DataDir+info.Name. Writing
	// anywhere else leaves the torrent with unverifiable bytes and
	// the subscriber's download stalls forever. This is the exact
	// production bug the F3 scenario caught when it first ran.
	pubOpts.Dir = pub.DataDir
	pubOpts.PublisherKey = pubkey

	cp, err := companion.NewPublisher(
		pub.Index, publisherPutter, pub.Eng, pubOpts, testLogger())
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	cp.Start()

	// Wait for the first refresh to complete. In regtest mode
	// the publisher interval is 10s, so the initial refresh
	// fires at startup (see publisher.run()). Give it up to
	// 10s to produce a non-empty status.
	var companionIH [20]byte
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		st := cp.Status()
		if st.LastInfoHash != "" && st.LastError == "" {
			decoded, err := hex.DecodeString(st.LastInfoHash)
			if err != nil || len(decoded) != 20 {
				t.Fatalf("bad LastInfoHash %q", st.LastInfoHash)
			}
			copy(companionIH[:], decoded)
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if companionIH == [20]byte{} {
		cp.Stop()
		t.Fatalf("publisher never produced an infohash; status=%+v", cp.Status())
	}
	t.Logf("publisher produced companion infohash %x", companionIH[:8])

	// Stop the publisher NOW, before the next refresh tick.
	// Every refresh rewrites the on-disk JSON atomically, which
	// invalidates the previous infohash's piece hashes if we're
	// still trying to serve them to the subscriber. Freezing the
	// publisher after the first infohash guarantees the bytes
	// backing companionIH stay put for the rest of the test.
	cp.Stop()

	// Force the publisher's engine to verify its local copy of
	// the companion torrent. anacrolix does lazy verification
	// after AddTorrent; if the subscriber connects before
	// verification completes, the publisher responds with an
	// empty bitfield and the download stalls. VerifyData()
	// synchronously hashes the single piece, which is cheap for
	// a ~300-byte file.
	pubHandle, err := pub.Eng.HandleByInfoHash(companionIH)
	if err != nil {
		t.Fatalf("publisher: HandleByInfoHash(%x): %v", companionIH[:8], err)
	}
	// 10 s is generous for a single-piece ~300-byte file but
	// accommodates a heavily loaded CI machine. VerifyDataContext
	// replaces the deprecated VerifyData call.
	verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := pubHandle.T.VerifyDataContext(verifyCtx); err != nil {
		t.Logf("warning: VerifyDataContext: %v", err)
	}
	verifyCancel()
	// Sanity: publisher should now report the torrent as fully
	// complete before the subscriber tries to download.
	if bc := pubHandle.T.BytesCompleted(); bc == 0 {
		t.Logf("warning: publisher BytesCompleted=0 after VerifyData; may race")
	}

	// ------------------------------------------------------------
	// 5. Pre-wire the subscriber's engine as a peer of the
	//    publisher for the companion torrent. The subscriber's
	//    engine has no DHT and no tracker, so without this
	//    step it cannot find anyone to download from. The
	//    anacrolix AddClientPeer call adds the publisher's
	//    listen address to the subscriber's torrent peer set.
	//
	//    Must happen BEFORE the subscriber's Sync runs, so the
	//    sub's AddInfoHash creates the torrent handle first.
	subHandle, err := sub.Eng.AddInfoHash(companionIH)
	if err != nil {
		t.Fatalf("sub.AddInfoHash: %v", err)
	}
	nAdded, err := sub.Eng.AddTrustedPeerEngine(companionIH, pub.Eng)
	if err != nil {
		t.Fatalf("sub.AddTrustedPeerEngine: %v", err)
	}
	t.Logf("wired %d peer addresses on subscriber for companion %x", nAdded, companionIH[:8])
	_ = subHandle // reserved for future stats logging

	// ------------------------------------------------------------
	// 6. Build the subscriber and run one Sync pass. The
	//    Sync call blocks on pointer resolution, torrent
	//    download, decode, and ingest. In regtest mode the
	//    whole pipeline should complete in well under the
	//    FetchTimeout default.
	subOpts := companion.DefaultSubscriberOptions()
	subOpts.FetchTimeout = 10 * time.Second
	subOpts.PointerTimeout = 2 * time.Second

	cs, err := companion.NewSubscriber(
		subscriberGetter, sub.Eng, sub.Index, subOpts, testLogger())
	if err != nil {
		t.Fatalf("NewSubscriber: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	res := cs.Sync(ctx, pubkey)
	if res.Err != nil {
		c.DumpLogs(t)
		t.Fatalf("Sync: %v", res.Err)
	}
	t.Logf("sync result: imported %d torrents, %d content rows from %s",
		res.TorrentsImported, res.ContentImported, res.Publisher)

	if res.TorrentsImported == 0 {
		t.Fatal("no torrents imported from publisher")
	}

	// ------------------------------------------------------------
	// 7. The subscriber's local index must now be able to
	//    answer a query for a keyword ONLY the publisher
	//    originally knew. This is the end-to-end assertion
	//    that proves the full F3 round trip worked.
	resp := sub.LocalQuery(t, "ubuntu")
	if len(resp.Hits) == 0 {
		c.DumpLogs(t)
		t.Fatal("subscriber index has no 'ubuntu' hit after F3 sync")
	}
	var matched bool
	for _, h := range resp.Hits {
		if h.Name == "ubuntu 24.04 desktop amd64 iso" {
			matched = true
			break
		}
	}
	if !matched {
		t.Errorf("subscriber index has ubuntu hits but not the expected one; hits=%+v", resp.Hits)
	}

	// Content-level hit (the "quick brown fox" body) should
	// also be searchable on the subscriber side.
	contentResp := sub.LocalQuery(t, "brown fox")
	if len(contentResp.Hits) == 0 {
		t.Error("subscriber index missing content-level hit after F3 sync")
	}
}

// testLogger returns a slog handler that discards output.
// Scenario tests don't want to pollute the test log with
// worker debug messages unless something fails, and the
// cluster's per-node LogBuf already captures engine-level
// events for DumpLogs.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
