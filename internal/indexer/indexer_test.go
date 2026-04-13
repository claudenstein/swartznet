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
			InfoHash:   "1111111111111111111111111111111111111111",
			FilePath:   "README.md",
			Text:       "jumps over the lazy dog", // 23 bytes
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

// TestDeleteTorrent exercises the remove-by-infohash path. After
// Delete the doc must no longer appear in Search results and
// DocCount must drop. A second delete for the same infohash is
// a no-op per the contract.
func TestDeleteTorrent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "index.bleve")
	idx, err := indexer.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	ih := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	if err := idx.IndexTorrent(indexer.TorrentDoc{
		InfoHash: ih,
		Name:     "to be deleted",
	}); err != nil {
		t.Fatal(err)
	}

	res, err := idx.Search(indexer.SearchRequest{Query: "deleted"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 {
		t.Fatalf("pre-delete hits=%d, want 1", len(res.Hits))
	}

	if err := idx.DeleteTorrent(ih); err != nil {
		t.Fatalf("DeleteTorrent: %v", err)
	}

	res, err = idx.Search(indexer.SearchRequest{Query: "deleted"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 0 {
		t.Errorf("post-delete hits=%d, want 0", len(res.Hits))
	}

	// Second delete for a missing infohash must not return an
	// error per the DeleteTorrent contract.
	if err := idx.DeleteTorrent(ih); err != nil {
		t.Errorf("second delete: %v", err)
	}
}

// TestAllTorrentDocsEmpty verifies AllTorrentDocs on an empty index
// returns a nil/empty slice with no error.
func TestAllTorrentDocsEmpty(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "index.bleve")

	idx, err := indexer.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	docs, err := idx.AllTorrentDocs()
	if err != nil {
		t.Fatalf("AllTorrentDocs: %v", err)
	}
	if len(docs) != 0 {
		t.Errorf("AllTorrentDocs on empty index returned %d docs, want 0", len(docs))
	}
}

// TestAllTorrentDocsSingle verifies AllTorrentDocs returns a single
// torrent doc with all fields correctly reconstructed from stored fields.
func TestAllTorrentDocsSingle(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "index.bleve")

	idx, err := indexer.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	added := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	want := indexer.TorrentDoc{
		InfoHash:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Name:      "ubuntu 24.04 desktop amd64 iso",
		FilePaths: []string{"ubuntu-24.04-desktop-amd64.iso", "README.diskdefines"},
		Trackers:  []string{"udp://tracker.example.org:1337/announce"},
		SizeBytes: 6 * 1024 * 1024 * 1024,
		FileCount: 2,
		AddedAt:   added,
	}
	if err := idx.IndexTorrent(want); err != nil {
		t.Fatalf("IndexTorrent: %v", err)
	}

	docs, err := idx.AllTorrentDocs()
	if err != nil {
		t.Fatalf("AllTorrentDocs: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("AllTorrentDocs returned %d docs, want 1", len(docs))
	}
	got := docs[0]
	if got.InfoHash != want.InfoHash {
		t.Errorf("InfoHash = %q, want %q", got.InfoHash, want.InfoHash)
	}
	if got.Name != want.Name {
		t.Errorf("Name = %q, want %q", got.Name, want.Name)
	}
	if got.SizeBytes != want.SizeBytes {
		t.Errorf("SizeBytes = %d, want %d", got.SizeBytes, want.SizeBytes)
	}
	if got.FileCount != want.FileCount {
		t.Errorf("FileCount = %d, want %d", got.FileCount, want.FileCount)
	}
	if len(got.FilePaths) != len(want.FilePaths) {
		t.Errorf("FilePaths len = %d, want %d", len(got.FilePaths), len(want.FilePaths))
	} else {
		for i := range want.FilePaths {
			if got.FilePaths[i] != want.FilePaths[i] {
				t.Errorf("FilePaths[%d] = %q, want %q", i, got.FilePaths[i], want.FilePaths[i])
			}
		}
	}
	if len(got.Trackers) != len(want.Trackers) {
		t.Errorf("Trackers len = %d, want %d", len(got.Trackers), len(want.Trackers))
	} else {
		for i := range want.Trackers {
			if got.Trackers[i] != want.Trackers[i] {
				t.Errorf("Trackers[%d] = %q, want %q", i, got.Trackers[i], want.Trackers[i])
			}
		}
	}
	if !got.AddedAt.Equal(want.AddedAt) {
		t.Errorf("AddedAt = %v, want %v", got.AddedAt, want.AddedAt)
	}
}

// TestAllTorrentDocsMultiple verifies AllTorrentDocs returns all indexed
// torrent docs when multiple are present. Also ensures content docs are
// excluded from the result.
func TestAllTorrentDocsMultiple(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "index.bleve")

	idx, err := indexer.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	hashes := []string{
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"cccccccccccccccccccccccccccccccccccccccc",
	}
	for _, h := range hashes {
		if err := idx.IndexTorrent(indexer.TorrentDoc{
			InfoHash: h,
			Name:     "torrent-" + h[:4],
		}); err != nil {
			t.Fatalf("IndexTorrent(%s): %v", h[:4], err)
		}
	}

	// Also index a content doc that must NOT appear in AllTorrentDocs.
	if err := idx.IndexContent(indexer.ContentDoc{
		InfoHash:  hashes[0],
		FilePath:  "readme.txt",
		Text:      "some extracted content",
		Extractor: "plaintext",
	}); err != nil {
		t.Fatalf("IndexContent: %v", err)
	}

	docs, err := idx.AllTorrentDocs()
	if err != nil {
		t.Fatalf("AllTorrentDocs: %v", err)
	}
	if len(docs) != 3 {
		t.Fatalf("AllTorrentDocs returned %d docs, want 3", len(docs))
	}

	// Collect returned infohashes into a set.
	got := make(map[string]bool, len(docs))
	for _, d := range docs {
		got[d.InfoHash] = true
	}
	for _, h := range hashes {
		if !got[h] {
			t.Errorf("AllTorrentDocs missing infohash %s", h[:8])
		}
	}
}

// TestContentDocsForInfoHashEmpty verifies that querying content docs
// for an infohash with no content returns an empty slice, no error.
func TestContentDocsForInfoHashEmpty(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "index.bleve")

	idx, err := indexer.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	docs, err := idx.ContentDocsForInfoHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("ContentDocsForInfoHash: %v", err)
	}
	if len(docs) != 0 {
		t.Errorf("expected 0 content docs, got %d", len(docs))
	}
}

// TestContentDocsForInfoHashRoundTrip indexes several content docs under
// two different infohashes, then retrieves by infohash and verifies that
// only the correct docs are returned with all fields reconstructed.
func TestContentDocsForInfoHashRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "index.bleve")

	idx, err := indexer.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	ih1 := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	ih2 := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	// Two content docs for ih1, one for ih2.
	ih1Docs := []indexer.ContentDoc{
		{
			InfoHash:  ih1,
			FileIndex: 0,
			FilePath:  "chapter1.txt",
			FileSize:  5000,
			Mime:      "text/plain",
			Text:      "the quick brown fox",
			Extractor: "plaintext",
		},
		{
			InfoHash:   ih1,
			FileIndex:  0,
			FilePath:   "chapter1.txt",
			FileSize:   5000,
			Mime:       "text/plain",
			Text:       "jumps over the lazy dog",
			Extractor:  "plaintext",
			ChunkIndex: 1,
		},
	}
	ih2Doc := indexer.ContentDoc{
		InfoHash:  ih2,
		FileIndex: 0,
		FilePath:  "notes.txt",
		FileSize:  200,
		Mime:      "text/plain",
		Text:      "unrelated text",
		Extractor: "plaintext",
	}

	for _, d := range ih1Docs {
		if err := idx.IndexContent(d); err != nil {
			t.Fatalf("IndexContent: %v", err)
		}
	}
	if err := idx.IndexContent(ih2Doc); err != nil {
		t.Fatalf("IndexContent: %v", err)
	}

	// Retrieve for ih1 — should get exactly 2 docs.
	got, err := idx.ContentDocsForInfoHash(ih1)
	if err != nil {
		t.Fatalf("ContentDocsForInfoHash(%s): %v", ih1[:8], err)
	}
	if len(got) != 2 {
		t.Fatalf("ContentDocsForInfoHash returned %d docs, want 2", len(got))
	}

	// Verify field reconstruction on retrieved docs.
	texts := make(map[string]bool)
	for _, d := range got {
		texts[d.Text] = true
		if d.InfoHash != ih1 {
			t.Errorf("doc InfoHash = %q, want %q", d.InfoHash, ih1)
		}
		if d.FilePath != "chapter1.txt" {
			t.Errorf("doc FilePath = %q, want chapter1.txt", d.FilePath)
		}
		if d.FileSize != 5000 {
			t.Errorf("doc FileSize = %d, want 5000", d.FileSize)
		}
		if d.Mime != "text/plain" {
			t.Errorf("doc Mime = %q, want text/plain", d.Mime)
		}
		if d.Extractor != "plaintext" {
			t.Errorf("doc Extractor = %q, want plaintext", d.Extractor)
		}
		if d.IndexedAt.IsZero() {
			t.Error("doc IndexedAt is zero, want non-zero")
		}
	}
	if !texts["the quick brown fox"] {
		t.Error("missing text 'the quick brown fox'")
	}
	if !texts["jumps over the lazy dog"] {
		t.Error("missing text 'jumps over the lazy dog'")
	}

	// Retrieve for ih2 — should get exactly 1 doc.
	got2, err := idx.ContentDocsForInfoHash(ih2)
	if err != nil {
		t.Fatalf("ContentDocsForInfoHash(%s): %v", ih2[:8], err)
	}
	if len(got2) != 1 {
		t.Fatalf("ContentDocsForInfoHash returned %d docs, want 1", len(got2))
	}
	if got2[0].Text != "unrelated text" {
		t.Errorf("doc Text = %q, want 'unrelated text'", got2[0].Text)
	}
}

// TestContentDocsForInfoHashCaseInsensitive verifies that the infohash
// lookup is case-insensitive (upper-case input still finds lower-cased
// stored docs).
func TestContentDocsForInfoHashCaseInsensitive(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "index.bleve")

	idx, err := indexer.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	ih := "aabbccddaabbccddaabbccddaabbccddaabbccdd"
	if err := idx.IndexContent(indexer.ContentDoc{
		InfoHash:  ih,
		FilePath:  "test.txt",
		Text:      "case test content",
		Extractor: "plaintext",
	}); err != nil {
		t.Fatal(err)
	}

	// Query with upper-case infohash.
	got, err := idx.ContentDocsForInfoHash(strings.ToUpper(ih))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(got))
	}
	if got[0].Text != "case test content" {
		t.Errorf("Text = %q, want 'case test content'", got[0].Text)
	}
}

// TestStatsEmptyIndex verifies all Stats fields on a fresh empty index.
// InflationRatio must be 0, and all counters must be 0.
func TestStatsEmptyIndex(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "index.bleve")

	idx, err := indexer.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	st, err := idx.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.TorrentCount != 0 {
		t.Errorf("TorrentCount = %d, want 0", st.TorrentCount)
	}
	if st.ContentCount != 0 {
		t.Errorf("ContentCount = %d, want 0", st.ContentCount)
	}
	if st.CorpusTextBytes != 0 {
		t.Errorf("CorpusTextBytes = %d, want 0", st.CorpusTextBytes)
	}
	if st.InflationRatio != 0 {
		t.Errorf("InflationRatio = %v, want 0", st.InflationRatio)
	}
	// DirBytes should be positive even for an empty index because Bleve
	// writes metadata files on creation.
	if st.DirBytes <= 0 {
		t.Errorf("DirBytes = %d, want > 0", st.DirBytes)
	}
	// DocCount may be 0 (no user docs).
	if st.DocCount != 0 {
		t.Errorf("DocCount = %d, want 0", st.DocCount)
	}
}

// TestStatsLargeCorpus verifies Stats with many content docs. This
// ensures the paginated CorpusTextBytes scan works correctly across
// more than one batch-worth of docs wouldn't be practical to test at
// batch=1000, so we verify the sum is correct for a moderate corpus.
func TestStatsLargeCorpus(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "index.bleve")

	idx, err := indexer.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	ih := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := idx.IndexTorrent(indexer.TorrentDoc{
		InfoHash: ih,
		Name:     "corpus test",
	}); err != nil {
		t.Fatal(err)
	}

	const numDocs = 50
	text := "abcdefghij" // 10 bytes each
	var wantBytes int64
	for i := 0; i < numDocs; i++ {
		if err := idx.IndexContent(indexer.ContentDoc{
			InfoHash:   ih,
			FileIndex:  i,
			FilePath:   "file.txt",
			Text:       text,
			Extractor:  "plaintext",
			ChunkIndex: i,
		}); err != nil {
			t.Fatalf("IndexContent #%d: %v", i, err)
		}
		wantBytes += int64(len(text))
	}

	st, err := idx.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.ContentCount != numDocs {
		t.Errorf("ContentCount = %d, want %d", st.ContentCount, numDocs)
	}
	if st.TorrentCount != 1 {
		t.Errorf("TorrentCount = %d, want 1", st.TorrentCount)
	}
	if st.CorpusTextBytes != wantBytes {
		t.Errorf("CorpusTextBytes = %d, want %d", st.CorpusTextBytes, wantBytes)
	}
	if st.InflationRatio <= 0 {
		t.Errorf("InflationRatio = %v, want > 0", st.InflationRatio)
	}
}

// TestAllTorrentDocsExcludesContent ensures that AllTorrentDocs never
// returns content-level documents, even when the index holds both types.
func TestAllTorrentDocsExcludesContent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "index.bleve")

	idx, err := indexer.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	ih := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	// Index only content docs — no torrent doc.
	for i := 0; i < 5; i++ {
		if err := idx.IndexContent(indexer.ContentDoc{
			InfoHash:   ih,
			FileIndex:  i,
			FilePath:   "file.txt",
			Text:       "some text here",
			Extractor:  "plaintext",
			ChunkIndex: i,
		}); err != nil {
			t.Fatal(err)
		}
	}

	docs, err := idx.AllTorrentDocs()
	if err != nil {
		t.Fatalf("AllTorrentDocs: %v", err)
	}
	if len(docs) != 0 {
		t.Errorf("AllTorrentDocs returned %d docs, want 0 (only content docs in index)", len(docs))
	}
}

// TestTorrentDocFieldReconstruction verifies that torrent docs indexed
// with multiple trackers and file paths are faithfully reconstructed
// by AllTorrentDocs (field round-trip through Bleve stored fields).
func TestTorrentDocFieldReconstruction(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "index.bleve")

	idx, err := indexer.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	want := indexer.TorrentDoc{
		InfoHash: "aabbccddeeff00112233aabbccddeeff00112233",
		Name:     "multi-tracker test",
		FilePaths: []string{
			"dir/file1.txt",
			"dir/file2.pdf",
			"dir/sub/file3.epub",
		},
		Trackers: []string{
			"udp://tracker.one.org:1337/announce",
			"udp://tracker.two.org:6969/announce",
		},
		SizeBytes: 123456789,
		FileCount: 3,
		AddedAt:   time.Date(2026, 1, 15, 8, 30, 0, 0, time.UTC),
	}

	if err := idx.IndexTorrent(want); err != nil {
		t.Fatal(err)
	}

	docs, err := idx.AllTorrentDocs()
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("got %d docs, want 1", len(docs))
	}
	got := docs[0]

	if got.InfoHash != want.InfoHash {
		t.Errorf("InfoHash = %q, want %q", got.InfoHash, want.InfoHash)
	}
	if got.Name != want.Name {
		t.Errorf("Name = %q, want %q", got.Name, want.Name)
	}
	if got.SizeBytes != want.SizeBytes {
		t.Errorf("SizeBytes = %d, want %d", got.SizeBytes, want.SizeBytes)
	}
	if got.FileCount != want.FileCount {
		t.Errorf("FileCount = %d, want %d", got.FileCount, want.FileCount)
	}
	if !got.AddedAt.Equal(want.AddedAt) {
		t.Errorf("AddedAt = %v, want %v", got.AddedAt, want.AddedAt)
	}

	// FilePaths round-trip through newline-join in toBleve and split in
	// torrentDocFromFields.
	if len(got.FilePaths) != len(want.FilePaths) {
		t.Fatalf("FilePaths len = %d, want %d", len(got.FilePaths), len(want.FilePaths))
	}
	for i := range want.FilePaths {
		if got.FilePaths[i] != want.FilePaths[i] {
			t.Errorf("FilePaths[%d] = %q, want %q", i, got.FilePaths[i], want.FilePaths[i])
		}
	}

	// Multiple trackers round-trip through Bleve's stored array.
	if len(got.Trackers) != len(want.Trackers) {
		t.Fatalf("Trackers len = %d, want %d", len(got.Trackers), len(want.Trackers))
	}
	trackerSet := make(map[string]bool, len(got.Trackers))
	for _, tr := range got.Trackers {
		trackerSet[tr] = true
	}
	for _, tr := range want.Trackers {
		if !trackerSet[tr] {
			t.Errorf("missing tracker %q in reconstructed doc", tr)
		}
	}
}
