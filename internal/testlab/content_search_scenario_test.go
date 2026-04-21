package testlab_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/swartznet/swartznet/internal/engine"
	"github.com/swartznet/swartznet/internal/indexer"
	"github.com/swartznet/swartznet/internal/testlab"
)

// TestScenarioContentExtractSearch proves the full
// seed → leech → extract → index → local-query pipeline works
// end-to-end on a single test run.
//
// What the scenario walks through:
//   1. Seeder writes a .txt payload containing a unique keyword
//      that no other test ever uses.
//   2. Seeder builds + adds a real .torrent, verifies pieces.
//   3. Leecher fetches by magnet URI, completes download.
//   4. Leecher's ingest goroutine (engine.ingestFileEvents)
//      forwards the on-disk file to the plaintext extractor via
//      the indexer.Pipeline.
//   5. Extractor emits a ContentDoc into the leecher's Bleve
//      index.
//   6. A local search for the unique keyword returns a hit.
//
// This is the first test in the project that asserts the
// extract-and-index path runs automatically after a real swarm
// download. Previous ingest tests seeded documents into the
// index directly (IndexContent) or ran the pipeline against
// hand-constructed FileInputs; nothing connected piece-complete
// events to a Bleve query result.
func TestScenarioContentExtractSearch(t *testing.T) {
	c := testlab.NewCluster(t, 2)
	seed := c.Nodes[0]
	leech := c.Nodes[1]

	// Keyword must be something the plaintext extractor will pass
	// through untouched and the Bleve analyzer won't stem into a
	// near-match for a common word. The digit suffix keeps it out
	// of any tokenizer stopword list.
	const keyword = "periwinkle-scenario-token-71"
	const fileName = "notes.txt"
	payload := []byte("this file exists only so the " + keyword +
		" shows up in the leech's bleve index after a real download.\n")

	// ------------------------------------------------------------
	// 1. Seed the payload into the seeder's DataDir.
	payloadPath := filepath.Join(seed.DataDir, fileName)
	if err := os.MkdirAll(seed.DataDir, 0o755); err != nil {
		t.Fatalf("mkdir seed DataDir: %v", err)
	}
	if err := os.WriteFile(payloadPath, payload, 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	// ------------------------------------------------------------
	// 2. Build and seed the torrent. Piece length must be a power
	//    of two ≥ 16 KiB per BEP-3, so use the minimum for this
	//    small payload.
	torrentPath := filepath.Join(t.TempDir(), "seed.torrent")
	_, mi, err := seed.Eng.CreateTorrentFile(engine.CreateTorrentOptions{
		Root:        payloadPath,
		PieceLength: 16 * 1024,
	}, torrentPath)
	if err != nil {
		t.Fatalf("CreateTorrentFile: %v", err)
	}
	seedHandle, err := seed.Eng.AddTorrentFile(torrentPath)
	if err != nil {
		t.Fatalf("seed.AddTorrentFile: %v", err)
	}
	// VerifyDataContext forces the single piece to hash before the
	// leech starts requesting; without it the transfer races with
	// lazy piece verification on a loaded CI machine.
	verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := seedHandle.T.VerifyDataContext(verifyCtx); err != nil {
		t.Logf("warning: VerifyDataContext: %v", err)
	}
	verifyCancel()

	// ------------------------------------------------------------
	// 3. Leech adds by magnet URI, wires the seeder as peer, and
	//    waits for a complete download.
	ih := mi.HashInfoBytes()
	magnetURI := metainfo.Magnet{
		InfoHash:    ih,
		DisplayName: fileName,
	}.String()
	leechHandle, err := leech.Eng.AddMagnet(magnetURI)
	if err != nil {
		t.Fatalf("leech.AddMagnet: %v", err)
	}
	if _, err := leech.Eng.AddTrustedPeerEngine(ih, seed.Eng); err != nil {
		t.Fatalf("leech.AddTrustedPeerEngine: %v", err)
	}

	gotInfoCtx, cancelGotInfo := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelGotInfo()
	select {
	case <-leechHandle.T.GotInfo():
	case <-gotInfoCtx.Done():
		c.DumpLogs(t)
		t.Fatalf("leech never got metadata: %v", gotInfoCtx.Err())
	}
	leechHandle.T.DownloadAll()

	completeCtx, cancelComplete := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelComplete()
	select {
	case <-leechHandle.T.Complete().On():
	case <-completeCtx.Done():
		c.DumpLogs(t)
		t.Fatalf("leech never completed: BytesCompleted=%d BytesMissing=%d err=%v",
			leechHandle.T.BytesCompleted(), leechHandle.T.BytesMissing(), completeCtx.Err())
	}

	// ------------------------------------------------------------
	// 4. Poll the leech's Bleve index until the extractor +
	//    pipeline have produced a hit for the unique keyword. The
	//    ingest path runs on its own goroutine so the query
	//    appearing after Complete is not instantaneous.
	ihHex := ih.HexString()
	deadline := time.Now().Add(15 * time.Second)
	var resp *indexer.SearchResponse
	for time.Now().Before(deadline) {
		resp, err = leech.Index.Search(indexer.SearchRequest{
			Query: keyword,
			Limit: 10,
		})
		if err != nil {
			t.Fatalf("leech.Index.Search: %v", err)
		}
		if hitMatches(resp, ihHex) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	c.DumpLogs(t)
	t.Fatalf("leech index never produced a hit for %q from infohash %s after download; last resp=%+v",
		keyword, ihHex, resp)
}

// hitMatches reports whether resp contains at least one hit
// attributed to the given infohash. Matches on the first 12 hex
// chars so truncated log-style identifiers also match.
func hitMatches(resp *indexer.SearchResponse, ihHex string) bool {
	if resp == nil {
		return false
	}
	prefix := ihHex
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	for _, h := range resp.Hits {
		if strings.HasPrefix(h.InfoHash, prefix) {
			return true
		}
	}
	return false
}
