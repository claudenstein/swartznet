package indexer

import (
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"
)

// pollPipelineProcessed polls until p.Stats(ih).Processed >= 1
// or the deadline expires. Avoids fixed sleeps that flake.
func pollPipelineProcessed(t *testing.T, p *Pipeline, ih string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p.Stats(ih).Processed >= 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("processed counter never advanced for %s", ih)
}

// TestPipelineHandleNoExtractorMatchSkipsCounter covers the
// `ex == nil → skipped++ + return` branch of handle. Submit a
// FileInput whose extension is recognised by no extractor;
// Dispatch returns nil; the per-infohash skipped counter must
// increment to exactly 1, OpenReader must not have been invoked.
func TestPipelineHandleNoExtractorMatchSkipsCounter(t *testing.T) {
	t.Parallel()
	idx, err := Open(filepath.Join(t.TempDir(), "p1.bleve"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	p := NewPipeline(idx, slog.New(slog.NewTextHandler(io.Discard, nil)), 0)
	p.Start()
	defer p.Stop()

	const ih = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if !p.Submit(FileInput{
		InfoHash: ih,
		Path:     "no-recognised-extension.unknownxyz",
		Size:     1024,
		OpenReader: func() (io.Reader, error) {
			t.Error("OpenReader must not be called when no extractor matches")
			return nil, errors.New("should not be called")
		},
	}) {
		t.Fatal("Submit returned false")
	}

	pollPipelineProcessed(t, p, ih)
	st := p.Stats(ih)
	if st.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1 for unknown-extension file", st.Skipped)
	}
	if st.Extracted != 0 {
		t.Errorf("Extracted = %d, want 0 for unknown-extension file", st.Extracted)
	}
}

// TestPipelineHandleOpenReaderError covers the
// `OpenReader err → failed++ + return` branch.
func TestPipelineHandleOpenReaderError(t *testing.T) {
	t.Parallel()
	idx, err := Open(filepath.Join(t.TempDir(), "p2.bleve"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	p := NewPipeline(idx, slog.New(slog.NewTextHandler(io.Discard, nil)), 0)
	p.Start()
	defer p.Stop()

	const ih = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	wantErr := errors.New("simulated open failure")
	if !p.Submit(FileInput{
		InfoHash:   ih,
		Path:       "blob.txt",
		Size:       64,
		OpenReader: func() (io.Reader, error) { return nil, wantErr },
	}) {
		t.Fatal("Submit returned false")
	}

	pollPipelineProcessed(t, p, ih)
	st := p.Stats(ih)
	if st.Failed != 1 {
		t.Errorf("Failed = %d, want 1 for OpenReader error", st.Failed)
	}
}
