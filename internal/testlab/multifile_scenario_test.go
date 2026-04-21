package testlab_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/swartznet/swartznet/internal/engine"
	"github.com/swartznet/swartznet/internal/indexer"
	"github.com/swartznet/swartznet/internal/testlab"
)

// TestScenarioMultiFileTorrent drives a realistic multi-file
// torrent through the full seed → leech → ingest pipeline.
// Nothing else in the suite does this — every existing scenario
// uses a single-file torrent, so multi-file edge cases (nested
// directories, per-file priorities, per-file ingest events,
// mixed text/binary mime dispatch) have zero regression cover.
//
// Layout of the published content:
//
//	<info.name>/
//	  readme.txt           (plain text, contains keywordTop)
//	  data/
//	    payload.bin        (binary blob — sniffed out by plaintext)
//	    nested/
//	      deep.txt         (plain text, contains keywordDeep)
//
// Assertions:
//  1. Every file on the leech's disk matches the seeder byte-for-
//     byte. Proves the file-storage layer handles nested dirs.
//  2. The leech's Bleve index answers a query for keywordTop and
//     keywordDeep with hits attributed to the right infohash.
//     Proves ingestFileEvents fires per file and the pipeline
//     dispatches the plaintext extractor twice rather than
//     stopping after the first file.
//  3. No query for the binary payload keyword succeeds, proving
//     the plaintext extractor correctly skipped the binary file
//     instead of polluting the index with garbage.
func TestScenarioMultiFileTorrent(t *testing.T) {
	c := testlab.NewCluster(t, 2)
	seed := c.Nodes[0]
	leech := c.Nodes[1]

	const (
		keywordTop  = "orange-mango-readme-73"
		keywordDeep = "raspberry-nested-deep-74"
		binaryTag   = "lavender-binary-75"
	)
	readmeBody := []byte("this file mentions " + keywordTop + " once.\n")
	deepBody := []byte("this nested file mentions " + keywordDeep + " once.\n")
	binBody := make([]byte, 16*1024)
	// Prefix a NUL byte so the plaintext extractor refuses to index
	// this file (its heuristic rejects NULs in the first 4 KiB).
	copy(binBody, []byte{0x00, 0x01, 0x02, 0x03})
	copy(binBody[100:], []byte(binaryTag))

	// ------------------------------------------------------------
	// 1. Lay out the content under seed.DataDir/multifile-root/.
	//    BuildFromFilePath walks the tree and hashes every file,
	//    so the directory name becomes info.name and file paths
	//    inside the torrent are relative to it.
	const rootName = "multifile-root"
	root := filepath.Join(seed.DataDir, rootName)
	files := map[string][]byte{
		"readme.txt":             readmeBody,
		"data/payload.bin":       binBody,
		"data/nested/deep.txt":   deepBody,
	}
	for rel, body := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, body, 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	// ------------------------------------------------------------
	// 2. Create and seed the torrent over the directory root.
	torrentPath := filepath.Join(t.TempDir(), "multifile.torrent")
	_, mi, err := seed.Eng.CreateTorrentFile(engine.CreateTorrentOptions{
		Root:        root,
		PieceLength: 16 * 1024,
	}, torrentPath)
	if err != nil {
		t.Fatalf("CreateTorrentFile: %v", err)
	}
	seedHandle, err := seed.Eng.AddTorrentFile(torrentPath)
	if err != nil {
		t.Fatalf("seed.AddTorrentFile: %v", err)
	}

	verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := seedHandle.T.VerifyDataContext(verifyCtx); err != nil {
		t.Logf("warning: VerifyDataContext: %v", err)
	}
	verifyCancel()
	if bm := seedHandle.T.BytesMissing(); bm != 0 {
		t.Fatalf("seeder BytesMissing = %d after verify, want 0", bm)
	}

	// ------------------------------------------------------------
	// 3. Leech fetches via magnet URI and waits for completion.
	ih := mi.HashInfoBytes()
	magnetURI := metainfo.Magnet{InfoHash: ih, DisplayName: rootName}.String()
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
		t.Fatalf("leech never completed: BytesCompleted=%d BytesMissing=%d",
			leechHandle.T.BytesCompleted(), leechHandle.T.BytesMissing())
	}

	// ------------------------------------------------------------
	// 4. Verify every file on the leech's disk matches the source.
	for rel, want := range files {
		got, err := os.ReadFile(filepath.Join(leech.DataDir, rootName, rel))
		if err != nil {
			t.Fatalf("read leech %s: %v", rel, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("leech %s mismatch: got %d bytes (sha256=%x), want %d bytes (sha256=%x)",
				rel, len(got), sha256.Sum256(got), len(want), sha256.Sum256(want))
		}
	}

	// ------------------------------------------------------------
	// 5. Poll the leech's Bleve index until both text files have
	//    been extracted + indexed. The pipeline is async so this
	//    must tolerate a grace window.
	ihHex := ih.HexString()
	waitForKeyword(t, c, leech.Index, keywordTop, ihHex)
	waitForKeyword(t, c, leech.Index, keywordDeep, ihHex)

	// ------------------------------------------------------------
	// 6. Binary file contents must NOT have been indexed. If they
	//    were, it means the plaintext extractor skipped its NUL
	//    heuristic or a future extractor started claiming .bin
	//    files.
	resp, err := leech.Index.Search(indexer.SearchRequest{Query: binaryTag, Limit: 5})
	if err != nil {
		t.Fatalf("leech.Index.Search for binaryTag: %v", err)
	}
	if hitMatches(resp, ihHex) {
		t.Errorf("binary file contents leaked into index: resp=%+v", resp)
	}
}

// waitForKeyword polls the given index until a search for the
// keyword returns a hit attributed to ihHex, or fails the test.
func waitForKeyword(t *testing.T, c *testlab.Cluster, idx *indexer.Index, keyword, ihHex string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var resp *indexer.SearchResponse
	for time.Now().Before(deadline) {
		var err error
		resp, err = idx.Search(indexer.SearchRequest{Query: keyword, Limit: 10})
		if err != nil {
			t.Fatalf("Search(%q): %v", keyword, err)
		}
		if hitMatches(resp, ihHex) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	c.DumpLogs(t)
	t.Fatalf("index never produced a hit for %q from infohash %s; last resp=%+v",
		keyword, ihHex, resp)
}
