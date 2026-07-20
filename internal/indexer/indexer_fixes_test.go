package indexer_test

import (
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/indexer"
)

// TestAllTorrentDocsPreservesSignedBy is the regression for the fix
// that adds fieldSignedBy to AllTorrentDocs' projection: the signer
// pubkey (schema-v3) must survive the index→TorrentDoc round trip,
// not get silently dropped.
func TestAllTorrentDocsPreservesSignedBy(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "idx"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	const signer = "deadbeef00000000000000000000000000000000000000000000000000000000"
	if err := idx.IndexTorrent(indexer.TorrentDoc{
		InfoHash: "1111111111111111111111111111111111111111",
		Name:     "signed release",
		SignedBy: signer,
	}); err != nil {
		t.Fatalf("IndexTorrent: %v", err)
	}

	docs, err := idx.AllTorrentDocs()
	if err != nil {
		t.Fatalf("AllTorrentDocs: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("got %d docs, want 1", len(docs))
	}
	if docs[0].SignedBy != signer {
		t.Errorf("SignedBy = %q, want %q (field dropped on reconstruction)", docs[0].SignedBy, signer)
	}
}

// TestContentDocsForInfoHashHostileInfoHash verifies the term-query
// fix: an infohash containing Bleve query metacharacters must not
// inject query syntax. We index content for a clean infohash, then
// query with an infohash carrying query operators and a wildcard —
// it must match nothing (exact term mismatch), never error or
// over-match via injected syntax.
func TestContentDocsForInfoHashHostileInfoHash(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "idx"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	const ih = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := idx.IndexContent(indexer.ContentDoc{
		InfoHash: ih,
		FilePath: "book.txt",
		Text:     "searchable body",
	}); err != nil {
		t.Fatalf("IndexContent: %v", err)
	}

	// Sanity: the legitimate lookup returns the doc.
	got, err := idx.ContentDocsForInfoHash(ih)
	if err != nil {
		t.Fatalf("ContentDocsForInfoHash(clean): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("clean lookup got %d docs, want 1", len(got))
	}

	// Hostile infohash with query operators / wildcard must not match.
	for _, hostile := range []string{
		"+type:content *",
		"aaaa* OR type:content",
		`a" OR "1`,
	} {
		got, err := idx.ContentDocsForInfoHash(hostile)
		if err != nil {
			t.Fatalf("ContentDocsForInfoHash(%q): unexpected error %v", hostile, err)
		}
		if len(got) != 0 {
			t.Errorf("hostile infohash %q matched %d docs — query injection not contained", hostile, len(got))
		}
	}
}

// TestDeleteContentForTorrentExactMatch confirms that the term-query
// delete only removes the targeted infohash's content (not another
// torrent's) and returns the right count — exercising the fixed
// (escaping-free, bounded) delete path.
func TestDeleteContentForTorrentExactMatch(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "idx"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	const keep = "1111111111111111111111111111111111111111"
	const drop = "2222222222222222222222222222222222222222"
	docs := []struct {
		ih string
		fi int
	}{
		{keep, 0},
		{drop, 0},
		{drop, 1},
	}
	for _, d := range docs {
		if err := idx.IndexContent(indexer.ContentDoc{
			InfoHash:  d.ih,
			FileIndex: d.fi, // distinct FileIndex => distinct doc IDs
			FilePath:  "f.txt",
			Text:      "text",
		}); err != nil {
			t.Fatalf("IndexContent: %v", err)
		}
	}

	n, err := idx.DeleteContentForTorrent(drop)
	if err != nil {
		t.Fatalf("DeleteContentForTorrent: %v", err)
	}
	if n != 2 {
		t.Fatalf("deleted %d docs, want 2", n)
	}
	remaining, err := idx.ContentDocsForInfoHash(keep)
	if err != nil {
		t.Fatalf("ContentDocsForInfoHash: %v", err)
	}
	if len(remaining) != 1 {
		t.Errorf("kept-torrent content = %d, want 1 (wrong torrent's content deleted)", len(remaining))
	}
}
