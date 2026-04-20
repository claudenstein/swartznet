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
		InfoHash:   "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		FileIndex:  0,
		Path:       "physics_notes.txt",
		Size:       int64(len(body)),
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

	const binaryIH = "ffffffffffffffffffffffffffffffffffffffff"
	p.Submit(indexer.FileInput{
		InfoHash:   binaryIH,
		FileIndex:  0,
		Path:       "movie.mkv",
		Size:       2 * 1024 * 1024 * 1024, // 2 GiB
		OpenReader: mockOpenReader("whatever"),
	})

	// Poll the pipeline's per-infohash Stats until Processed==1,
	// which tells us the dispatcher ran and classified the file.
	// More robust than a fixed sleep on slow CI.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s := p.Stats(binaryIH); s.Processed >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if s := p.Stats(binaryIH); s.Processed != 1 || s.Skipped != 1 {
		t.Fatalf("stats after binary submit: %+v, want Processed=1 Skipped=1", s)
	}

	after, _ := idx.DocCount()
	if after != before {
		t.Errorf("DocCount changed: before=%d, after=%d; expected no change for binary input", before, after)
	}
}

// TestPipelineSubmitAfterStop verifies that Submit returns false after
// Stop has been called, once the internal input channel buffer is full.
// The Pipeline's input channel has a fixed buffer (64). After Stop, the
// worker goroutine has exited and nothing drains the channel, so once
// the buffer is saturated the only ready select case is the closed
// stopCh, guaranteeing Submit returns false.
func TestPipelineSubmitAfterStop(t *testing.T) {
	t.Parallel()

	idx, err := indexer.Open(filepath.Join(t.TempDir(), "index.bleve"))
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	defer idx.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := indexer.NewPipeline(idx, log, 0)
	p.Start()
	p.Stop()

	// Fill the buffered input channel (cap=64). After Stop the worker
	// goroutine has exited, so nothing drains the channel. Some sends
	// may return false (stopCh wins the select race), so we keep
	// sending until the buffer is truly saturated. The return value
	// is intentionally ignored here — the point is to saturate the
	// buffer, not to assert on each Submit.
	for i := 0; i < 256; i++ {
		_ = p.Submit(indexer.FileInput{
			InfoHash:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			FileIndex:  i,
			Path:       "fill.txt",
			Size:       1,
			OpenReader: mockOpenReader("x"),
		})
	}

	// Now the buffer is full (64 successful sends out of 256 attempts
	// is overwhelmingly likely). The send case cannot proceed, so the
	// only ready case in Submit's select is <-p.stopCh. Verify across
	// multiple calls for confidence.
	for i := 0; i < 10; i++ {
		ok := p.Submit(indexer.FileInput{
			InfoHash:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			FileIndex:  1000 + i,
			Path:       "overflow.txt",
			Size:       10,
			OpenReader: mockOpenReader("hello world"),
		})
		if ok {
			t.Errorf("Submit #%d returned true after Stop with full buffer; want false", i)
		}
	}
}

// TestPipelineStatsCounters verifies the per-infohash progress counters
// that power the GUI's indexing progress bar. A text file should land in
// Extracted, a binary file (no matching extractor) in Skipped, and both
// should advance Processed. A distinct infohash must stay at zero.
func TestPipelineStatsCounters(t *testing.T) {
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

	const ih = "cccccccccccccccccccccccccccccccccccccccc"
	const other = "dddddddddddddddddddddddddddddddddddddddd"

	// Unknown infohash — zero-valued snapshot.
	if ps := p.Stats("nonesuch"); ps.Processed != 0 || ps.Extracted != 0 {
		t.Errorf("Stats(nonesuch) = %+v, want zero-valued", ps)
	}

	// One text file and one binary file, same torrent.
	p.Submit(indexer.FileInput{
		InfoHash:   ih,
		FileIndex:  0,
		Path:       "notes.txt",
		Size:       42,
		OpenReader: mockOpenReader("progress bar demo content"),
	})
	p.Submit(indexer.FileInput{
		InfoHash:   ih,
		FileIndex:  1,
		Path:       "clip.mkv",
		Size:       1 << 30,
		OpenReader: mockOpenReader("binary junk"),
	})

	// Poll until both files have been processed (async worker).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if p.Stats(ih).Processed >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	ps := p.Stats(ih)
	if ps.Processed != 2 {
		t.Errorf("Processed = %d, want 2", ps.Processed)
	}
	if ps.Extracted != 1 {
		t.Errorf("Extracted = %d, want 1", ps.Extracted)
	}
	if ps.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", ps.Skipped)
	}
	if ps.Failed != 0 {
		t.Errorf("Failed = %d, want 0", ps.Failed)
	}

	// Counters are partitioned by infohash.
	if other := p.Stats(other); other.Processed != 0 {
		t.Errorf("Stats(other).Processed = %d, want 0", other.Processed)
	}

	// Case-insensitive lookup — caller may pass uppercase hex.
	if upper := p.Stats("CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC"); upper.Processed != 2 {
		t.Errorf("uppercase Stats.Processed = %d, want 2", upper.Processed)
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
			InfoHash:  "1111111111111111111111111111111111111111",
			FileIndex: 0,
			FilePath:  "book.txt",
			FileSize:  1000,
			Mime:      "text/plain",
			Text:      "chapter one was about regression",
			Extractor: "plaintext",
		},
		{
			InfoHash:  "1111111111111111111111111111111111111111",
			FileIndex: 1,
			FilePath:  "appendix.txt",
			FileSize:  500,
			Mime:      "text/plain",
			Text:      "appendix discusses eigenvectors",
			Extractor: "plaintext",
		},
		{
			InfoHash:  "2222222222222222222222222222222222222222",
			FileIndex: 0,
			FilePath:  "other.txt",
			FileSize:  200,
			Mime:      "text/plain",
			Text:      "unrelated content about regression",
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
