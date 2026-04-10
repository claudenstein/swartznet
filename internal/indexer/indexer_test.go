package indexer_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/indexer"
)

// TestRoundTrip indexes two torrents, queries them by distinguishing terms,
// and verifies that the expected infohash comes back on top.
func TestRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "index.bleve")

	idx, err := indexer.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	docs := []indexer.TorrentDoc{
		{
			InfoHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Name:     "ubuntu 24.04 desktop amd64 iso",
			FilePaths: []string{
				"ubuntu-24.04-desktop-amd64.iso",
				"README.diskdefines",
			},
			Trackers:  []string{"udp://tracker.example.org:1337/announce"},
			SizeBytes: 6 * 1024 * 1024 * 1024,
			AddedAt:   time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC),
		},
		{
			InfoHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			Name:     "debian bookworm netinst",
			FilePaths: []string{
				"debian-12.5.0-amd64-netinst.iso",
				"SHA256SUMS",
			},
			Trackers:  []string{"udp://tracker.debian.org:6969/announce"},
			SizeBytes: 500 * 1024 * 1024,
			AddedAt:   time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, d := range docs {
		if err := idx.IndexTorrent(d); err != nil {
			t.Fatalf("IndexTorrent(%s): %v", d.Name, err)
		}
	}

	if n, err := idx.DocCount(); err != nil || n != 2 {
		t.Fatalf("DocCount = %d, %v; want 2, nil", n, err)
	}

	cases := []struct {
		query       string
		wantInfoHas string // prefix match on infohash is enough
	}{
		{"ubuntu", "aaaaaaaa"},
		{"debian", "bbbbbbbb"},
		{"netinst", "bbbbbbbb"},
		{"name:iso", "aaaaaaaa"}, // both match, but ubuntu has the term in name
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			res, err := idx.Search(indexer.SearchRequest{Query: tc.query})
			if err != nil {
				t.Fatalf("Search(%q): %v", tc.query, err)
			}
			if len(res.Hits) == 0 {
				t.Fatalf("Search(%q): zero hits", tc.query)
			}
			top := res.Hits[0].InfoHash
			if len(top) < len(tc.wantInfoHas) || top[:len(tc.wantInfoHas)] != tc.wantInfoHas {
				t.Errorf("Search(%q) top = %s; want prefix %s", tc.query, top, tc.wantInfoHas)
			}
		})
	}
}

// TestUpdateSemantics verifies that re-indexing the same infohash replaces
// the earlier document rather than creating a duplicate.
func TestUpdateSemantics(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "index.bleve")

	idx, err := indexer.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	orig := indexer.TorrentDoc{
		InfoHash: "cccccccccccccccccccccccccccccccccccccccc",
		Name:     "old name",
	}
	if err := idx.IndexTorrent(orig); err != nil {
		t.Fatal(err)
	}

	updated := orig
	updated.Name = "new name"
	if err := idx.IndexTorrent(updated); err != nil {
		t.Fatal(err)
	}

	if n, err := idx.DocCount(); err != nil || n != 1 {
		t.Fatalf("DocCount = %d, %v; want 1, nil", n, err)
	}

	res, err := idx.Search(indexer.SearchRequest{Query: `name:"new name"`})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 || res.Hits[0].Name != "new name" {
		t.Errorf("search after update: hits=%v", res.Hits)
	}
}

// TestReopenPersistsDocuments checks that closing and reopening an index on
// the same path preserves previously-indexed documents.
func TestReopenPersistsDocuments(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "index.bleve")

	idx, err := indexer.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.IndexTorrent(indexer.TorrentDoc{
		InfoHash: "dddddddddddddddddddddddddddddddddddddddd",
		Name:     "persistent",
	}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := indexer.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	if n, err := reopened.DocCount(); err != nil || n != 1 {
		t.Fatalf("reopened DocCount = %d, %v; want 1, nil", n, err)
	}
}
