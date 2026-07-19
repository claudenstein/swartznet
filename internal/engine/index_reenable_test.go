package engine

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/indexer"
)

// TestSetTorrentIndexingReenableWritesTorrentDoc pins the round-5 LOW fix: the
// torrent-level Layer-L document is written by exactly one runtime path
// (autoIndex, once per handle). If indexing was off when metadata arrived, the
// doc is absent; enabling indexing at runtime must re-write it. Before the fix
// SetTorrentIndexing only flipped the flag, leaving the torrent unsearchable by
// name until a daemon restart.
func TestSetTorrentIndexingReenableWritesTorrentDoc(t *testing.T) {
	e, dataDir := seedEngine(t, time.Hour)
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "index"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	e.SetIndex(idx)

	const name = "uniquetorrentxyzzz"
	root := filepath.Join(dataDir, "doc")
	mi := buildTextTorrent(t, root, name, []byte("body of a unique torrent"))
	h, err := e.AddTorrentMetaInfoSeedFrom(mi, filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}

	present := func() bool {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			res, err := idx.Search(indexer.SearchRequest{Query: name, Limit: 10})
			if err == nil && res.Total > 0 {
				return true
			}
			time.Sleep(10 * time.Millisecond)
		}
		return false
	}

	if !present() {
		t.Fatal("autoIndex never wrote the torrent doc")
	}

	// Simulate the doc being absent because indexing was off at metadata time.
	if err := e.SetTorrentIndexing(h.InfoHashHex(), false); err != nil {
		t.Fatal(err)
	}
	if err := idx.DeleteTorrent(h.InfoHashHex()); err != nil {
		t.Fatal(err)
	}
	if res, _ := idx.Search(indexer.SearchRequest{Query: name, Limit: 10}); res.Total != 0 {
		t.Fatalf("precondition: torrent doc still present (%d hits)", res.Total)
	}

	// Re-enabling indexing must re-write the torrent doc.
	if err := e.SetTorrentIndexing(h.InfoHashHex(), true); err != nil {
		t.Fatal(err)
	}
	if !present() {
		t.Error("re-enabling SetTorrentIndexing did not re-write the torrent-level doc")
	}
}
