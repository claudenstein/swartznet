package companion_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/companion"
	"github.com/swartznet/swartznet/internal/indexer"
)

// seedIndex creates a temporary Bleve index pre-populated with
// two torrents and a handful of content docs across them, so
// the BuildFromIndex tests have realistic input without
// running the M2 ingestion pipeline.
func seedIndex(t *testing.T) *indexer.Index {
	t.Helper()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "idx"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })

	docs := []indexer.TorrentDoc{
		{
			InfoHash:  "1111111111111111111111111111111111111111",
			Name:      "Ubuntu 24.04",
			FilePaths: []string{"README.md", "boot/vmlinuz"},
			SizeBytes: 6 << 30,
			AddedAt:   time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC),
		},
		{
			InfoHash:  "2222222222222222222222222222222222222222",
			Name:      "Debian Bookworm",
			FilePaths: []string{"README.txt"},
			SizeBytes: 500 << 20,
		},
	}
	for _, d := range docs {
		if err := idx.IndexTorrent(d); err != nil {
			t.Fatal(err)
		}
	}

	contents := []indexer.ContentDoc{
		{
			InfoHash:  "1111111111111111111111111111111111111111",
			FileIndex: 0,
			FilePath:  "README.md",
			FileSize:  1024,
			Mime:      "text/markdown",
			Extractor: "plaintext",
			Text:      "the quick brown fox jumps over the lazy dog",
		},
		{
			InfoHash:   "1111111111111111111111111111111111111111",
			FileIndex:  0,
			FilePath:   "README.md",
			FileSize:   1024,
			Mime:       "text/markdown",
			Extractor:  "plaintext",
			Text:       "second extracted chunk from the same file",
			ChunkIndex: 1,
		},
		{
			InfoHash:  "2222222222222222222222222222222222222222",
			FileIndex: 0,
			FilePath:  "README.txt",
			FileSize:  256,
			Mime:      "text/plain",
			Extractor: "plaintext",
			Text:      "debian bookworm release notes",
		},
	}
	for _, d := range contents {
		if err := idx.IndexContent(d); err != nil {
			t.Fatal(err)
		}
	}
	return idx
}

func TestBuildFromIndexBasic(t *testing.T) {
	t.Parallel()
	idx := seedIndex(t)

	out, err := companion.BuildFromIndex(idx, "abcdef00", companion.DefaultBuildOptions())
	if err != nil {
		t.Fatal(err)
	}
	if out.Publisher != "abcdef00" {
		t.Errorf("Publisher = %q, want abcdef00", out.Publisher)
	}
	if out.GeneratedAt == 0 {
		t.Errorf("GeneratedAt was not set")
	}
	if len(out.Torrents) != 2 {
		t.Fatalf("len(Torrents) = %d, want 2", len(out.Torrents))
	}

	// Find Ubuntu (the one with content). Order is not
	// guaranteed by Bleve so we look it up by infohash.
	var ubuntu *companion.TorrentRecord
	for i := range out.Torrents {
		if out.Torrents[i].InfoHash == "1111111111111111111111111111111111111111" {
			ubuntu = &out.Torrents[i]
		}
	}
	if ubuntu == nil {
		t.Fatal("Ubuntu torrent missing from output")
	}
	if ubuntu.Name != "Ubuntu 24.04" {
		t.Errorf("Name = %q, want 'Ubuntu 24.04'", ubuntu.Name)
	}
	if len(ubuntu.Files) != 2 {
		t.Fatalf("len(Files) = %d, want 2 (one with content + one without)", len(ubuntu.Files))
	}
	// File 0 should have 2 chunks (collated from the two
	// content docs we indexed for it).
	var readme *companion.FileRecord
	for i := range ubuntu.Files {
		if ubuntu.Files[i].Path == "README.md" {
			readme = &ubuntu.Files[i]
		}
	}
	if readme == nil {
		t.Fatal("README.md FileRecord missing")
	}
	if len(readme.Chunks) != 2 {
		t.Errorf("README chunks = %d, want 2", len(readme.Chunks))
	}
	if readme.Mime != "text/markdown" {
		t.Errorf("README mime = %q, want text/markdown", readme.Mime)
	}
}

func TestBuildFromIndexExcludeContent(t *testing.T) {
	t.Parallel()
	idx := seedIndex(t)
	opts := companion.DefaultBuildOptions()
	opts.IncludeContent = false

	out, err := companion.BuildFromIndex(idx, "", opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range out.Torrents {
		if len(r.Files) != 0 {
			t.Errorf("torrent %s has %d files, want 0 with IncludeContent=false",
				r.InfoHash, len(r.Files))
		}
	}
}

func TestBuildFromIndexMaxChunks(t *testing.T) {
	t.Parallel()
	idx := seedIndex(t)
	opts := companion.DefaultBuildOptions()
	opts.MaxChunksPerFile = 1

	out, err := companion.BuildFromIndex(idx, "", opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range out.Torrents {
		for _, f := range r.Files {
			if len(f.Chunks) > 1 {
				t.Errorf("file %s has %d chunks, MaxChunksPerFile=1", f.Path, len(f.Chunks))
			}
		}
	}
}

func TestBuildFromIndexNilIndex(t *testing.T) {
	t.Parallel()
	if _, err := companion.BuildFromIndex(nil, "x", companion.DefaultBuildOptions()); err == nil {
		t.Error("expected error for nil index")
	}
}

func TestWriteCompanionFilesRoundTrip(t *testing.T) {
	t.Parallel()
	idx := seedIndex(t)

	dir := t.TempDir()
	out, err := companion.BuildFromIndex(idx, "abcd", companion.DefaultBuildOptions())
	if err != nil {
		t.Fatal(err)
	}
	jsonPath, mi, err := companion.WriteCompanionFiles(dir, out)
	if err != nil {
		t.Fatal(err)
	}
	if jsonPath == "" || mi == nil {
		t.Fatal("WriteCompanionFiles returned an empty result")
	}

	// Verify both files exist on disk and are non-empty.
	for _, p := range []string{jsonPath, filepath.Join(dir, "companion.torrent")} {
		st, err := lstat(t, p)
		if err != nil {
			t.Errorf("%s: %v", p, err)
			continue
		}
		if st == 0 {
			t.Errorf("%s is empty", p)
		}
	}

	// Verify the metainfo's info hash is non-zero and the
	// info dictionary parses cleanly.
	info, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatalf("UnmarshalInfo: %v", err)
	}
	if info.Length == 0 {
		t.Errorf("info.Length is 0")
	}
	if info.Name == "" {
		t.Errorf("info.Name is empty")
	}
	if len(info.Pieces)%20 != 0 {
		t.Errorf("info.Pieces has length %d, not a multiple of 20", len(info.Pieces))
	}
	ih := mi.HashInfoBytes()
	var zero [20]byte
	if ih == zero {
		t.Error("infohash is all zeros")
	}
}

// lstat is a tiny test helper that returns the file size or an
// error. Avoids importing os in every test.
func lstat(t *testing.T, path string) (int64, error) {
	t.Helper()
	st, err := osStat(path)
	if err != nil {
		return 0, err
	}
	return st, nil
}
