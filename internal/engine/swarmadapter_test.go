package engine

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/indexer"
)

// TestSwarmSenderMissingPeer verifies that swarmSender.Send returns an
// error when the requested peer address is not in the peerTracker.
func TestSwarmSenderMissingPeer(t *testing.T) {
	t.Parallel()

	pt := newPeerTracker()
	sender := &swarmSender{peers: pt}

	err := sender.Send("192.168.1.1:6881", []byte("hello"))
	if err == nil {
		t.Fatal("expected error for missing peer, got nil")
	}
	if !strings.Contains(err.Error(), "no peer with addr") {
		t.Errorf("error = %q, want it to mention missing peer", err)
	}
}

// TestSwarmSenderMissingPeerMultipleAddrs checks that different missing
// addresses all produce errors (not some stale cached result).
func TestSwarmSenderMissingPeerMultipleAddrs(t *testing.T) {
	t.Parallel()

	pt := newPeerTracker()
	sender := &swarmSender{peers: pt}

	addrs := []string{
		"10.0.0.1:6881",
		"[::1]:6881",
		"192.168.0.1:51413",
	}
	for _, addr := range addrs {
		err := sender.Send(addr, []byte("payload"))
		if err == nil {
			t.Errorf("Send(%q): expected error, got nil", addr)
		}
	}
}

// TestPeerTrackerAddRemoveGet exercises the peerTracker's basic
// add/get/remove lifecycle without needing real PeerConns. We only
// check the bool return of get since we can't easily construct a real
// *torrent.PeerConn in a unit test.
func TestPeerTrackerAddRemoveGet(t *testing.T) {
	t.Parallel()

	pt := newPeerTracker()

	// Initially empty.
	if _, ok := pt.get("1.2.3.4:6881"); ok {
		t.Error("get on empty tracker returned ok=true")
	}

	// Add a nil PeerConn (we only care about the map lookup, not the value).
	pt.add("1.2.3.4:6881", nil)

	if _, ok := pt.get("1.2.3.4:6881"); !ok {
		t.Error("get after add returned ok=false")
	}

	// Different address still absent.
	if _, ok := pt.get("5.6.7.8:6881"); ok {
		t.Error("get for different addr returned ok=true")
	}

	// Remove and verify gone.
	pt.remove("1.2.3.4:6881")
	if _, ok := pt.get("1.2.3.4:6881"); ok {
		t.Error("get after remove returned ok=true")
	}
}

// TestIndexerSearcherSearchLocal creates a real Bleve index, indexes a
// torrent document, and verifies that indexerSearcher.SearchLocal
// returns the correct LocalHit fields.
func TestIndexerSearcherSearchLocal(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "test.bleve")
	idx, err := indexer.Open(path)
	if err != nil {
		t.Fatalf("indexer.Open: %v", err)
	}
	defer idx.Close()

	doc := indexer.TorrentDoc{
		InfoHash:  "cccccccccccccccccccccccccccccccccccccccc",
		Name:      "archlinux 2026.04 x86_64 iso",
		FilePaths: []string{"archlinux-2026.04.01-x86_64.iso"},
		SizeBytes: 1024 * 1024 * 800,
		AddedAt:   time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC),
	}
	if err := idx.IndexTorrent(doc); err != nil {
		t.Fatalf("IndexTorrent: %v", err)
	}

	searcher := &indexerSearcher{idx: idx}
	total, hits, err := searcher.SearchLocal("archlinux", 10)
	if err != nil {
		t.Fatalf("SearchLocal: %v", err)
	}
	if total == 0 {
		t.Fatal("SearchLocal returned total=0, want at least 1")
	}
	if len(hits) == 0 {
		t.Fatal("SearchLocal returned no hits")
	}

	hit := hits[0]
	if hit.InfoHash != doc.InfoHash {
		t.Errorf("InfoHash = %q, want %q", hit.InfoHash, doc.InfoHash)
	}
	if hit.Name != doc.Name {
		t.Errorf("Name = %q, want %q", hit.Name, doc.Name)
	}
	if hit.SizeBytes != doc.SizeBytes {
		t.Errorf("SizeBytes = %d, want %d", hit.SizeBytes, doc.SizeBytes)
	}
	if hit.DocType != "torrent" {
		t.Errorf("DocType = %q, want %q", hit.DocType, "torrent")
	}
	if hit.Score <= 0 {
		t.Errorf("Score = %f, want > 0", hit.Score)
	}
}

// TestIndexerSearcherEmptyQuery verifies that SearchLocal propagates the
// error from indexer.Search when given an empty query string.
func TestIndexerSearcherEmptyQuery(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "empty.bleve")
	idx, err := indexer.Open(path)
	if err != nil {
		t.Fatalf("indexer.Open: %v", err)
	}
	defer idx.Close()

	searcher := &indexerSearcher{idx: idx}
	_, _, err = searcher.SearchLocal("", 10)
	if err == nil {
		t.Fatal("expected error for empty query, got nil")
	}
}

// TestIndexerSearcherNoResults verifies that SearchLocal returns zero
// hits and total=0 when the query matches nothing.
func TestIndexerSearcherNoResults(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "noresults.bleve")
	idx, err := indexer.Open(path)
	if err != nil {
		t.Fatalf("indexer.Open: %v", err)
	}
	defer idx.Close()

	// Index a document so the index is non-empty.
	doc := indexer.TorrentDoc{
		InfoHash:  "dddddddddddddddddddddddddddddddddddddddd"[:40],
		Name:      "fedora workstation live",
		SizeBytes: 2 * 1024 * 1024 * 1024,
		AddedAt:   time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC),
	}
	if err := idx.IndexTorrent(doc); err != nil {
		t.Fatalf("IndexTorrent: %v", err)
	}

	searcher := &indexerSearcher{idx: idx}
	total, hits, err := searcher.SearchLocal("nonexistentxyzzy", 10)
	if err != nil {
		t.Fatalf("SearchLocal: %v", err)
	}
	if total != 0 {
		t.Errorf("total = %d, want 0", total)
	}
	if len(hits) != 0 {
		t.Errorf("hits = %d, want 0", len(hits))
	}
}
