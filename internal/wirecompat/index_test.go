package wirecompat

// Layer-L wiring tests: they drive engine → fileTracker → pipeline →
// extraction against a real Bleve index. Deterministic single-engine, so
// CI -race safe (each opens Bleve once, ~1s).

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/indexer"
)

func openTestIndex(t *testing.T) *indexer.Index {
	t.Helper()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "index"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	return idx
}

// searchContentByExtractor polls the index for a content hit whose extractor
// matches, returning the first hit's fragments/infohash.
func waitForContentHit(t *testing.T, idx *indexer.Index, query, wantExtractor string) indexer.SearchHit {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		resp, err := idx.Search(indexer.SearchRequest{Query: query, Limit: 10, Highlight: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range resp.Hits {
			if h.DocType == "content" && (wantExtractor == "" || h.Extractor == wantExtractor) {
				return h
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for content hit %q (extractor=%q)", query, wantExtractor)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestPlaintextIndexedThroughLivePipeline: a seeded .txt file is extracted,
// chunked, indexed, and searchable with a <mark> fragment.
func TestPlaintextIndexedThroughLivePipeline(t *testing.T) {
	c := NewCluster(t, 1)
	node := c.Nodes[0]
	idx := openTestIndex(t)
	node.Eng.SetIndex(idx)

	body := []byte(strings.Repeat("the archive preserves knowledge. ", 200) + "swartznetmarker payload here.")
	content := filepath.Join(node.DataDir, "content")
	mi, _ := BuildFixtureBytes(t, content, "book.txt", body)
	if _, err := node.Eng.AddTorrentMetaInfoSeedFrom(mi, filepath.Join(content, "book.txt")); err != nil {
		t.Fatal(err)
	}
	hit := waitForContentHit(t, idx, "swartznetmarker", "plaintext")
	frag := ""
	for _, f := range hit.Fragments["text"] {
		frag += f
	}
	if !strings.Contains(frag, "<mark>swartznetmarker</mark>") {
		t.Fatalf("fragment lacks <mark> highlight: %q", hit.Fragments)
	}
}

// TestZimIndexedThroughLivePipeline is THE §6 regression gate: a .zim file
// seeded by the engine is extracted through the ReadSeekerAt shim (anacrolix
// readers are seek-only, never ReaderAt), producing extractor=zim content
// docs. The legacy passed the reader through bare and every daemon .zim
// silently "skipped".
func TestZimIndexedThroughLivePipeline(t *testing.T) {
	c := NewCluster(t, 1)
	node := c.Nodes[0]
	idx := openTestIndex(t)
	node.Eng.SetIndex(idx)

	zim := buildTestZimBlob(t, "zimuniquemarker inside the encyclopedia article body")
	content := filepath.Join(node.DataDir, "content")
	mi, _ := BuildFixtureBytes(t, content, "wiki.zim", zim)
	h, err := node.Eng.AddTorrentMetaInfoSeedFrom(mi, filepath.Join(content, "wiki.zim"))
	if err != nil {
		t.Fatal(err)
	}
	hit := waitForContentHit(t, idx, "zimuniquemarker", "zim")
	if hit.Mime != "application/x-zim" {
		t.Fatalf("zim hit mime = %q, want application/x-zim", hit.Mime)
	}
	// Pipeline counters must show extracted, not skipped.
	proc, ext := node.Eng.IndexStats(h.InfoHashHex())
	if ext < 1 {
		t.Fatalf("zim extracted=%d of processed=%d — the live pipeline skipped it", ext, proc)
	}
}

// TestMultiFileEachIndexedFromOwnBytes pins the size-bounded reader fix: in a
// multi-file torrent every file must be extracted from ONLY its own bytes.
// anacrolix's File.NewReader over-reads a large buffer past the file end into
// the following files, so without the size bound the first files see a
// concatenation (a text file sees the later binary bytes and is skipped).
func TestMultiFileEachIndexedFromOwnBytes(t *testing.T) {
	c := NewCluster(t, 1)
	node := c.Nodes[0]
	idx := openTestIndex(t)
	node.Eng.SetIndex(idx)

	// Three small text files: a size-bounded reader indexes all three; an
	// unbounded reader over-reads file 0/1 into file 2's bytes.
	dir := filepath.Join(node.DataDir, "multi")
	files := map[string]string{
		"a.txt": "alphamarker unique first",
		"b.txt": "betamarker unique second",
		"c.txt": "gammamarker unique third",
	}
	mi := BuildMultiFileTorrent(t, dir, files)
	if _, err := node.Eng.AddTorrentMetaInfoSeedFrom(mi, dir); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"alphamarker", "betamarker", "gammamarker"} {
		waitForContentHit(t, idx, marker, "plaintext")
	}
}

// TestIndexingOffSurvivesRestart pins the restore-race fix: an indexing=OFF
// torrent restored over a fresh index must NOT be re-indexed (its persisted
// OFF flag is applied before the index goroutines spawn), while an ON torrent
// IS re-indexed — a deterministic gate on the ordering bug the review found.
func TestIndexingOffSurvivesRestart(t *testing.T) {
	c := NewCluster(t, 1)
	node := c.Nodes[0]

	off := filepath.Join(node.DataDir, "off")
	on := filepath.Join(node.DataDir, "on")
	miOff, _ := BuildFixtureBytes(t, off, "secret.txt", []byte("offmarker must not be indexed after restart."))
	miOn, _ := BuildFixtureBytes(t, on, "public.txt", []byte("onmarker should be indexed after restart."))

	hOff, err := node.Eng.AddTorrentMetaInfoSeedFrom(miOff, filepath.Join(off, "secret.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := node.Eng.AddTorrentMetaInfoSeedFrom(miOn, filepath.Join(on, "public.txt")); err != nil {
		t.Fatal(err)
	}
	<-hOff.T.GotInfo()
	if err := node.Eng.SetTorrentIndexing(hOff.InfoHashHex(), false); err != nil {
		t.Fatal(err)
	}
	if err := node.Eng.Close(); err != nil {
		t.Fatal(err)
	}

	// Restart over the same DataDir with a FRESH index. Restore applies OFF
	// before autoIndex can run, so the OFF torrent contributes nothing while
	// the ON torrent re-indexes.
	eng2, err := engineNew(t, node.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	idx2 := openTestIndex(t)
	eng2.SetIndex(idx2)
	if err := eng2.RestoreSession(); err != nil {
		t.Fatal(err)
	}
	// The ON torrent must reappear (proves restore + autoIndex ran).
	waitFor(t, 15*time.Second, "ON torrent re-indexed", func() bool {
		resp, _ := idx2.Search(indexer.SearchRequest{Query: "onmarker", Limit: 5})
		return resp != nil && resp.Total > 0
	})
	// The OFF torrent must NOT — no torrent doc, no content doc.
	offResp, err := idx2.Search(indexer.SearchRequest{Query: "secret.txt offmarker", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range offResp.Hits {
		if h.InfoHash == hOff.InfoHashHex() {
			t.Fatalf("OFF torrent was re-indexed on restart (restore race): %+v", h)
		}
	}
}

// TestForgetDeletesDocsKeepsFiles pins the reachable Forget: RemoveTorrent
// with forget deletes the index docs while the file stays on disk.
func TestForgetDeletesDocsKeepsFiles(t *testing.T) {
	c := NewCluster(t, 1)
	node := c.Nodes[0]
	idx := openTestIndex(t)
	node.Eng.SetIndex(idx)

	content := filepath.Join(node.DataDir, "content")
	body := []byte(strings.Repeat("forgettable text ", 200) + " forgetmarker end.")
	mi, _ := BuildFixtureBytes(t, content, "gone.txt", body)
	h, err := node.Eng.AddTorrentMetaInfoSeedFrom(mi, filepath.Join(content, "gone.txt"))
	if err != nil {
		t.Fatal(err)
	}
	ih := h.InfoHashHex()
	waitForContentHit(t, idx, "forgetmarker", "plaintext")

	if err := node.Eng.RemoveTorrent(ih); err != nil {
		t.Fatal(err)
	}
	node.Eng.ForgetIndex(ih)

	resp, err := idx.Search(indexer.SearchRequest{Query: "forgetmarker", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Total != 0 {
		t.Fatalf("Forget left %d hits", resp.Total)
	}
	// File is still on disk.
	if _, err := readFile(filepath.Join(content, "gone.txt")); err != nil {
		t.Fatalf("Forget deleted the file: %v", err)
	}
}
