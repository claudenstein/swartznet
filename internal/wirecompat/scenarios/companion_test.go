package scenarios

import (
	"context"
	"encoding/hex"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/companion"
	"github.com/swartznet/swartznet/internal/indexer"
	"github.com/swartznet/swartznet/internal/wirecompat"
)

// fakePointerGetter stands in for the DHT BEP-46 pointer read: the cluster runs
// with the DHT off, so the pointer is delivered directly. The full Layer-D
// BEP-44 pointer round-trip is covered by the dhtindex cluster test.
type fakePointerGetter struct {
	pub [32]byte
	ih  [20]byte
}

func (g *fakePointerGetter) GetInfohashPointer(_ context.Context, pub [32]byte, _ []byte) ([20]byte, error) {
	if pub != g.pub {
		return [20]byte{}, context.Canceled
	}
	return g.ih, nil
}

// TestCompanionPublishFollowImport is THE Slice 10 DoD scenario: node A builds
// and seeds a companion index; node B fetches it over real BitTorrent through
// the FAIL-CLOSED engine fetch, verifies the snapshot was authored by A, and
// imports the records into its local index STAMPED with SignedBy = A's pubkey.
func TestCompanionPublishFollowImport(t *testing.T) {
	if testing.Short() {
		t.Skip("multi-client transfer; run without -short")
	}
	c := wirecompat.NewCluster(t, 2)
	nodeA, nodeB := c.Nodes[0], c.Nodes[1]
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// A's identity (the companion author).
	var pubA [32]byte
	for i := range pubA {
		pubA[i] = byte(0xA0 + i)
	}
	pubAHex := hex.EncodeToString(pubA[:])

	// Build A's companion index by hand (one torrent with content) and write +
	// seed it in place from A's companion dir.
	torIH := strings.Repeat("ab", 20) // 40-hex
	idx := companion.CompanionIndex{
		Publisher:   pubAHex,
		GeneratedAt: 1700000000,
		Torrents: []companion.TorrentRecord{{
			InfoHash: torIH, Name: "ubuntu 24.04 desktop", Size: 4096,
			Files: []companion.FileRecord{{Index: 0, Path: "notes.txt", Mime: "text/plain",
				Extractor: "plaintext", Chunks: []companion.ContentChunk{{Text: "the quick brown fox"}}}},
		}},
	}
	dirA := filepath.Join(nodeA.DataDir, "companion")
	jsonPath, mi, err := companion.WriteCompanionFiles(dirA, idx)
	if err != nil {
		t.Fatal(err)
	}
	hSeed, err := nodeA.Eng.AddTorrentMetaInfoSeedFrom(mi, jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	companionIH := mi.HashInfoBytes()
	waitFor(t, 10*time.Second, "A seeds the companion torrent", func() bool {
		return hSeed.T.Info() != nil && hSeed.T.BytesCompleted() == hSeed.T.Length()
	})

	// Pre-wire B → A for the companion infohash (DHT is off). FetchCompanionTorrent
	// dedupes onto this already-peered handle.
	if _, err := nodeB.Eng.AddInfoHash(companionIH); err != nil {
		t.Fatal(err)
	}
	if _, err := nodeB.Eng.AddTrustedPeerEngine(companionIH, nodeA.Eng); err != nil {
		t.Fatal(err)
	}

	// B's local index (the import sink).
	idxB, err := indexer.Open(filepath.Join(nodeB.DataDir, "index-b"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idxB.Close() })

	var ihArr [20]byte
	copy(ihArr[:], companionIH[:])
	sub, err := companion.NewSubscriber(
		&fakePointerGetter{pub: pubA, ih: ihArr},
		nodeB.Eng, // the real FAIL-CLOSED fetcher
		idxB,      // CorpusImport
		companion.DefaultSubscriberOptions(),
		log,
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res := sub.Sync(ctx, pubA)
	if res.Err != nil {
		t.Fatalf("B sync failed: %v", res.Err)
	}
	if res.TorrentsImported != 1 || res.ContentImported != 1 {
		t.Fatalf("imported t=%d c=%d, want 1/1", res.TorrentsImported, res.ContentImported)
	}

	// The imported torrent doc must be stamped SignedBy = A's pubkey, and a
	// search restricted to A's pubkey must return it.
	docs, err := idxB.AllTorrentDocs()
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].SignedBy != pubAHex {
		t.Fatalf("imported doc = %+v (want SignedBy %s)", docs, pubAHex)
	}
	resp, err := idxB.Search(indexer.SearchRequest{Query: "ubuntu", SignedBy: pubAHex, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Total == 0 {
		t.Error("search --signed-by A returned nothing for the imported torrent")
	}

	// A second sync of the unchanged snapshot must dedup (no re-import).
	res2 := sub.Sync(ctx, pubA)
	if !res2.Deduped {
		t.Errorf("unchanged snapshot re-imported instead of dedup: %+v", res2)
	}
}
