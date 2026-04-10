package indexer_test

import (
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/indexer"
)

// mockOpenReader returns a FileInput.OpenReader that yields the given
// string content. Used to feed the pipeline without any real torrent.
func mockOpenReader(body string) func() (io.Reader, error) {
	return func() (io.Reader, error) {
		return strings.NewReader(body), nil
	}
}

// TestPipelineRoundTrip builds a Pipeline against a real Bleve index in a
// temp directory, submits a plain-text file, and verifies the content
// becomes findable via Search.
func TestPipelineRoundTrip(t *testing.T) {
	t.Parallel()

	idx, err := indexer.Open(filepath.Join(t.TempDir(), "index.bleve"))
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	defer idx.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := indexer.NewPipeline(idx, log, 0)
	p.Start()
	defer p.Stop()

	body := "quantum mechanics is the study of subatomic particles and their interactions"
	ok := p.Submit(indexer.FileInput{
		InfoHash:  "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		FileIndex: 0,
		Path:      "physics_notes.txt",
		Size:      int64(len(body)),
		OpenReader: mockOpenReader(body),
	})
	if !ok {
		t.Fatal("Submit returned false — pipeline closed unexpectedly")
	}

	// The pipeline runs asynchronously; poll for the content to appear
	// in the index, with a generous timeout for slow CI.
	deadline := time.Now().Add(5 * time.Second)
	var found bool
	for time.Now().Before(deadline) {
		res, err := idx.Search(indexer.SearchRequest{Query: "quantum"})
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range res.Hits {
			if h.DocType == "content" && h.InfoHash == "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee" {
				found = true
				if h.FilePath != "physics_notes.txt" {
					t.Errorf("FilePath = %q, want physics_notes.txt", h.FilePath)
				}
				if h.Extractor != "plaintext" {
					t.Errorf("Extractor = %q, want plaintext", h.Extractor)
				}
				break
			}
		}
		if found {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !found {
		t.Fatal("content document did not become searchable within 5s")
	}

	// A multi-word query that only matches content should still work.
	res, err := idx.Search(indexer.SearchRequest{Query: "subatomic particles"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total == 0 {
		t.Error("multi-word content search returned no hits")
	}
}

// TestPipelineSkipsBinary verifies that a file flagged as binary (dispatch
// returns no extractor) does not produce a content doc.
func TestPipelineSkipsBinary(t *testing.T) {
	t.Parallel()

	idx, err := indexer.Open(filepath.Join(t.TempDir(), "index.bleve"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := indexer.NewPipeline(idx, log, 0)
	p.Start()
	defer p.Stop()

	before, _ := idx.DocCount()

	p.Submit(indexer.FileInput{
		InfoHash:  "ffffffffffffffffffffffffffffffffffffffff",
		FileIndex: 0,
		Path:      "movie.mkv",
		Size:      2 * 1024 * 1024 * 1024, // 2 GiB
		OpenReader: mockOpenReader("whatever"),
	})

	// Give the pipeline a moment to reject.
	time.Sleep(150 * time.Millisecond)

	after, _ := idx.DocCount()
	if after != before {
		t.Errorf("DocCount changed: before=%d, after=%d; expected no change for binary input", before, after)
	}
}

// TestIndexContentAndDelete exercises the direct IndexContent /
// DeleteContentForTorrent path without the pipeline.
func TestIndexContentAndDelete(t *testing.T) {
	t.Parallel()

	idx, err := indexer.Open(filepath.Join(t.TempDir(), "index.bleve"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	docs := []indexer.ContentDoc{
		{
			InfoHash: "1111111111111111111111111111111111111111",
			FileIndex: 0,
			FilePath: "book.txt",
			FileSize: 1000,
			Mime:     "text/plain",
			Text:     "chapter one was about regression",
			Extractor: "plaintext",
		},
		{
			InfoHash: "1111111111111111111111111111111111111111",
			FileIndex: 1,
			FilePath: "appendix.txt",
			FileSize: 500,
			Mime:     "text/plain",
			Text:     "appendix discusses eigenvectors",
			Extractor: "plaintext",
		},
		{
			InfoHash: "2222222222222222222222222222222222222222",
			FileIndex: 0,
			FilePath: "other.txt",
			FileSize: 200,
			Mime:     "text/plain",
			Text:     "unrelated content about regression",
			Extractor: "plaintext",
		},
	}
	for _, d := range docs {
		if err := idx.IndexContent(d); err != nil {
			t.Fatalf("IndexContent: %v", err)
		}
	}

	// Both torrents mention "regression" in content; should get 2 hits.
	res, err := idx.Search(indexer.SearchRequest{Query: "regression"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 2 {
		t.Errorf("Total after insert = %d, want 2", res.Total)
	}

	// Delete all content belonging to the first torrent.
	n, err := idx.DeleteContentForTorrent("1111111111111111111111111111111111111111")
	if err != nil {
		t.Fatalf("DeleteContentForTorrent: %v", err)
	}
	if n != 2 {
		t.Errorf("deleted = %d, want 2", n)
	}

	res, err = idx.Search(indexer.SearchRequest{Query: "regression"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 1 {
		t.Errorf("Total after delete = %d, want 1", res.Total)
	}
	if len(res.Hits) != 1 || res.Hits[0].InfoHash != "2222222222222222222222222222222222222222" {
		t.Errorf("remaining hit = %+v, want infohash 2222...", res.Hits)
	}
}
