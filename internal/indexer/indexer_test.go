package indexer_test

import (
	"path/filepath"
	"strings"
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

// TestSearchHighlight pins down the M12e snippet-highlighting
// path: SearchRequest.Highlight=true yields Fragments wrapped in
// <mark>...</mark> HTML. Fragments are keyed by Bleve field
// name.
func TestSearchHighlight(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "index.bleve")
	idx, err := indexer.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	if err := idx.IndexTorrent(indexer.TorrentDoc{
		InfoHash: "1111111111111111111111111111111111111111",
		Name:     "ubuntu 24.04 desktop amd64 iso",
	}); err != nil {
		t.Fatal(err)
	}
	if err := idx.IndexContent(indexer.ContentDoc{
		InfoHash: "1111111111111111111111111111111111111111",
		FilePath: "README.md",
		Text:     "The quick brown fox jumps over the lazy dog.",
	}); err != nil {
		t.Fatal(err)
	}

	// Highlight off: Fragments must be nil on every hit.
	plain, err := idx.Search(indexer.SearchRequest{Query: "fox", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range plain.Hits {
		if h.Fragments != nil {
			t.Errorf("Highlight=false hit has Fragments=%v", h.Fragments)
		}
	}

	// Highlight on: content hit must carry a text fragment with
	// a <mark>fox</mark> wrapper.
	res, err := idx.Search(indexer.SearchRequest{
		Query:     "fox",
		Limit:     5,
		Highlight: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var content *indexer.SearchHit
	for i := range res.Hits {
		if res.Hits[i].DocType == "content" {
			content = &res.Hits[i]
			break
		}
	}
	if content == nil {
		t.Fatal("no content hit returned")
	}
	if content.Fragments == nil || len(content.Fragments["text"]) == 0 {
		t.Fatalf("no text fragments: %+v", content.Fragments)
	}
	if !strings.Contains(content.Fragments["text"][0], "<mark>fox</mark>") {
		t.Errorf("fragment missing <mark>fox</mark> wrapper: %q", content.Fragments["text"][0])
	}

	// Torrent hit on a name match should carry a name fragment.
	nameRes, err := idx.Search(indexer.SearchRequest{
		Query:     "ubuntu",
		Limit:     5,
		Highlight: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(nameRes.Hits) == 0 {
		t.Fatal("no hits for ubuntu")
	}
	var torrent *indexer.SearchHit
	for i := range nameRes.Hits {
		if nameRes.Hits[i].DocType == "torrent" {
			torrent = &nameRes.Hits[i]
			break
		}
	}
	if torrent == nil {
		t.Fatal("no torrent hit for ubuntu")
	}
	if len(torrent.Fragments["name"]) == 0 {
		t.Errorf("no name fragment on torrent hit: %+v", torrent.Fragments)
	} else if !strings.Contains(torrent.Fragments["name"][0], "<mark>ubuntu</mark>") {
		t.Errorf("name fragment missing mark: %q", torrent.Fragments["name"][0])
	}
}

// TestSearchQueryOperators pins down the Bleve QueryString
// operators that Search advertises in its docstring. Any
// regression here means the public search contract just broke.
func TestSearchQueryOperators(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "index.bleve")
	idx, err := indexer.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// t.Cleanup (not defer) because the sub-tests below are
	// t.Parallel() — a defer would fire before they resume.
	t.Cleanup(func() { _ = idx.Close() })

	docs := []indexer.TorrentDoc{
		{
			InfoHash: "1111111111111111111111111111111111111111",
			Name:     "ubuntu 24.04 desktop amd64 iso",
			FilePaths: []string{
				"ubuntu-24.04-desktop-amd64.iso",
				"README.diskdefines",
			},
			Trackers: []string{"udp://tracker.example.org/announce"},
		},
		{
			InfoHash: "2222222222222222222222222222222222222222",
			Name:     "debian bookworm netinst amd64 iso",
			FilePaths: []string{
				"debian-12.5.0-amd64-netinst.iso",
				"SHA256SUMS",
			},
			Trackers: []string{"udp://tracker.debian.org/announce"},
		},
		{
			InfoHash:  "3333333333333333333333333333333333333333",
			Name:      "alpine linux virt",
			FilePaths: []string{"alpine-3.19-virt-x86_64.iso"},
		},
	}
	for _, d := range docs {
		if err := idx.IndexTorrent(d); err != nil {
			t.Fatalf("IndexTorrent: %v", err)
		}
	}

	// Each case captures a prefix of the infohash we expect to
	// find in the result set. wantMiss is a list of prefixes that
	// MUST NOT appear.
	cases := []struct {
		name     string
		query    string
		wantHave []string // prefixes that must appear
		wantMiss []string // prefixes that must NOT appear
	}{
		{
			name:     "bare term",
			query:    "alpine",
			wantHave: []string{"3333"},
		},
		{
			name:     "required term excludes non-matches",
			query:    "+alpine",
			wantHave: []string{"3333"},
			wantMiss: []string{"1111", "2222"},
		},
		{
			name:     "negative term excludes docs",
			query:    "iso -debian -alpine",
			wantHave: []string{"1111"},
			wantMiss: []string{"2222", "3333"},
		},
		{
			name:     "phrase match uses term order",
			query:    `"desktop amd64"`,
			wantHave: []string{"1111"},
			wantMiss: []string{"2222", "3333"},
		},
		{
			name:     "fielded query restricts to name",
			query:    "name:bookworm",
			wantHave: []string{"2222"},
			wantMiss: []string{"1111", "3333"},
		},
		{
			name:     "fielded query on keyword-analyzed field",
			query:    "infohash:2222222222222222222222222222222222222222",
			wantHave: []string{"2222"},
			wantMiss: []string{"1111", "3333"},
		},
		{
			name:     "boolean AND (two required terms)",
			query:    "+amd64 +netinst",
			wantHave: []string{"2222"},
			wantMiss: []string{"1111", "3333"},
		},
		{
			name:     "fuzzy match tolerates one typo",
			query:    "ubunto~1", // one edit from "ubuntu"
			wantHave: []string{"1111"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := idx.Search(indexer.SearchRequest{Query: tc.query, Limit: 10})
			if err != nil {
				t.Fatalf("Search(%q): %v", tc.query, err)
			}
			found := make(map[string]bool)
			for _, h := range res.Hits {
				if len(h.InfoHash) >= 4 {
					found[h.InfoHash[:4]] = true
				}
			}
			for _, want := range tc.wantHave {
				if !found[want] {
					t.Errorf("Search(%q) missing expected prefix %s; got hits=%+v", tc.query, want, res.Hits)
				}
			}
			for _, miss := range tc.wantMiss {
				if found[miss] {
					t.Errorf("Search(%q) wrongly returned prefix %s; hits=%+v", tc.query, miss, res.Hits)
				}
			}
		})
	}
}

// TestIndexStats covers the M12b index-size measurement endpoint.
// Indexes two torrents and three content docs, then asserts every
// field on the Stats snapshot — doc counts by type, corpus text
// bytes, non-zero dir size, and a sensible inflation ratio.
func TestIndexStats(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "index.bleve")

	idx, err := indexer.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	// Empty index: everything zero except DirBytes (Bleve
	// writes schema metadata on create).
	empty, err := idx.Stats()
	if err != nil {
		t.Fatalf("Stats (empty): %v", err)
	}
	if empty.TorrentCount != 0 || empty.ContentCount != 0 {
		t.Errorf("empty counts not zero: %+v", empty)
	}
	if empty.InflationRatio != 0 {
		t.Errorf("empty InflationRatio = %v, want 0", empty.InflationRatio)
	}

	// Add two torrents + three content docs.
	torrents := []indexer.TorrentDoc{
		{InfoHash: "1111111111111111111111111111111111111111", Name: "ubuntu"},
		{InfoHash: "2222222222222222222222222222222222222222", Name: "debian"},
	}
	for _, d := range torrents {
		if err := idx.IndexTorrent(d); err != nil {
			t.Fatalf("IndexTorrent: %v", err)
		}
	}
	contents := []indexer.ContentDoc{
		{
			InfoHash: "1111111111111111111111111111111111111111",
			FilePath: "README.md",
			Text:     "the quick brown fox", // 19 bytes
		},
		{
			InfoHash: "1111111111111111111111111111111111111111",
			FilePath: "README.md",
			Text:     "jumps over the lazy dog", // 23 bytes
			ChunkIndex: 1,
		},
		{
			InfoHash: "2222222222222222222222222222222222222222",
			FilePath: "notes.txt",
			Text:     "hello world", // 11 bytes
		},
	}
	var wantText int64
	for _, d := range contents {
		wantText += int64(len(d.Text))
		if err := idx.IndexContent(d); err != nil {
			t.Fatalf("IndexContent: %v", err)
		}
	}

	st, err := idx.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.TorrentCount != 2 {
		t.Errorf("TorrentCount = %d, want 2", st.TorrentCount)
	}
	if st.ContentCount != 3 {
		t.Errorf("ContentCount = %d, want 3", st.ContentCount)
	}
	if st.CorpusTextBytes != wantText {
		t.Errorf("CorpusTextBytes = %d, want %d", st.CorpusTextBytes, wantText)
	}
	if st.DirBytes <= 0 {
		t.Errorf("DirBytes = %d, want positive", st.DirBytes)
	}
	// Inflation ratio should be a reasonable positive float
	// (Bleve writes a lot of metadata for a handful of tiny
	// docs, so we only assert the ratio is >0 and finite).
	if st.InflationRatio <= 0 {
		t.Errorf("InflationRatio = %v, want >0", st.InflationRatio)
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
