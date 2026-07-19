package indexer_test

import (
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/indexer"
)

// signedByOf fetches the stored signed_by for one infohash via a
// SignedBy-agnostic query, failing the test on lookup problems.
func signedByOf(t *testing.T, idx *indexer.Index, infohash string) string {
	t.Helper()
	docs, err := idx.AllTorrentDocs()
	if err != nil {
		t.Fatalf("AllTorrentDocs: %v", err)
	}
	for _, d := range docs {
		if d.InfoHash == infohash {
			return d.SignedBy
		}
	}
	t.Fatalf("torrent %s not found in index", infohash[:8])
	return ""
}

// TestIndexTorrentStickySignedBy pins the §7-Q37 rebuild fix: a later
// upsert with an empty SignedBy (local magnet re-add, companion import)
// must NOT blank a non-empty stored signed_by. Legacy was plain
// last-write-wins; the stickiness is enforced inside IndexTorrent so
// every writer crossing the seam is covered.
func TestIndexTorrentStickySignedBy(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "idx"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	const ih = "1111111111111111111111111111111111111111"
	const signer = "a000000000000000000000000000000000000000000000000000000000000000"

	// Signed write first.
	if err := idx.IndexTorrent(indexer.TorrentDoc{
		InfoHash: ih, Name: "signed release", SignedBy: signer,
	}); err != nil {
		t.Fatalf("signed IndexTorrent: %v", err)
	}
	if got := signedByOf(t, idx, ih); got != signer {
		t.Fatalf("after signed write: SignedBy = %q, want %q", got, signer)
	}

	// Unsigned upsert of the same infohash must carry the signer forward.
	if err := idx.IndexTorrent(indexer.TorrentDoc{
		InfoHash: ih, Name: "re-added via magnet",
	}); err != nil {
		t.Fatalf("unsigned re-index: %v", err)
	}
	if got := signedByOf(t, idx, ih); got != signer {
		t.Errorf("after unsigned upsert: SignedBy = %q, want sticky %q", got, signer)
	}

	// The rest of the doc IS replaced (still last-write-wins).
	docs, err := idx.AllTorrentDocs()
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].Name != "re-added via magnet" {
		t.Errorf("doc after upsert = %+v, want replaced Name", docs)
	}

	// The sticky value must also satisfy the SignedBy term filter.
	res, err := idx.Search(indexer.SearchRequest{SignedBy: signer})
	if err != nil {
		t.Fatalf("SignedBy search: %v", err)
	}
	if res.Total != 1 {
		t.Errorf("SignedBy filter total = %d, want 1 (sticky value searchable)", res.Total)
	}
}

// TestIndexTorrentSignedByNonEmptyOverwrites confirms stickiness only
// guards against blanking: a later NON-empty signer replaces the stored
// one (shared-namespace last-write-wins, DECISIONS §7-Q37).
func TestIndexTorrentSignedByNonEmptyOverwrites(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "idx"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	const ih = "2222222222222222222222222222222222222222"
	const first = "a000000000000000000000000000000000000000000000000000000000000000"
	const second = "b000000000000000000000000000000000000000000000000000000000000000"

	for _, signer := range []string{first, second} {
		if err := idx.IndexTorrent(indexer.TorrentDoc{
			InfoHash: ih, Name: "contested", SignedBy: signer,
		}); err != nil {
			t.Fatalf("IndexTorrent(%s): %v", signer[:4], err)
		}
	}
	if got := signedByOf(t, idx, ih); got != second {
		t.Errorf("SignedBy = %q, want %q (non-empty overwrite must win)", got, second)
	}
}

// TestIndexTorrentPreserveExistingSignerBlocksHijack is the regression for the
// companion authorship-hijack: a companion import (PreserveExistingSigner) must
// NOT overwrite a torrent already attributed to a DIFFERENT publisher, but a new
// torrent it introduces IS attributed to that publisher.
func TestIndexTorrentPreserveExistingSignerBlocksHijack(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "idx"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	const trusted = "a000000000000000000000000000000000000000000000000000000000000000"
	const attacker = "b000000000000000000000000000000000000000000000000000000000000000"

	// A torrent legitimately attributed to `trusted`.
	const ih = "3333333333333333333333333333333333333333"
	if err := idx.IndexTorrent(indexer.TorrentDoc{InfoHash: ih, Name: "x", SignedBy: trusted}); err != nil {
		t.Fatal(err)
	}
	// A followed publisher `attacker` lists ih in its companion snapshot. The
	// import must NOT hijack the attribution.
	if err := idx.IndexTorrent(indexer.TorrentDoc{InfoHash: ih, Name: "x", SignedBy: attacker, PreserveExistingSigner: true}); err != nil {
		t.Fatal(err)
	}
	if got := signedByOf(t, idx, ih); got != trusted {
		t.Errorf("SignedBy = %q, want %q — a companion import hijacked an existing attribution", got, trusted)
	}

	// A genuinely NEW torrent introduced by the companion import IS attributed to it.
	const ih2 = "4444444444444444444444444444444444444444"
	if err := idx.IndexTorrent(indexer.TorrentDoc{InfoHash: ih2, Name: "y", SignedBy: attacker, PreserveExistingSigner: true}); err != nil {
		t.Fatal(err)
	}
	if got := signedByOf(t, idx, ih2); got != attacker {
		t.Errorf("new-torrent SignedBy = %q, want %q", got, attacker)
	}
}

// TestIndexTorrentUnsignedStaysUnsigned covers the base case: with no
// prior doc (or an unsigned one), an unsigned write stores "".
func TestIndexTorrentUnsignedStaysUnsigned(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "idx"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	const ih = "3333333333333333333333333333333333333333"
	for range 2 {
		if err := idx.IndexTorrent(indexer.TorrentDoc{
			InfoHash: ih, Name: "unsigned",
		}); err != nil {
			t.Fatalf("IndexTorrent: %v", err)
		}
	}
	if got := signedByOf(t, idx, ih); got != "" {
		t.Errorf("SignedBy = %q, want empty for never-signed torrent", got)
	}
}
