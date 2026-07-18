package indexer

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync/atomic"
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

// TestPipelineHandleEmptyChunksSkipsCounter covers the
// `if len(chunks) == 0 { skipped++; return }` arm of handle.
// Submit a .txt file whose OpenReader returns an empty
// stream; the plaintext extractor returns no chunks, so
// handle increments Skipped without indexing anything.
func TestPipelineHandleEmptyChunksSkipsCounter(t *testing.T) {
	t.Parallel()
	idx, err := Open(filepath.Join(t.TempDir(), "p_empty.bleve"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	p := NewPipeline(idx, slog.New(slog.NewTextHandler(io.Discard, nil)), 0)
	p.Start()
	defer p.Stop()

	const ih = "dddddddddddddddddddddddddddddddddddddddd"
	if !p.Submit(FileInput{
		InfoHash: ih,
		Path:     "empty.txt",
		Size:     0,
		OpenReader: func() (io.Reader, error) {
			return bytes.NewReader(nil), nil
		},
	}) {
		t.Fatal("Submit returned false")
	}
	pollPipelineProcessed(t, p, ih)
	st := p.Stats(ih)
	if st.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1 for empty-chunks file", st.Skipped)
	}
	if st.Extracted != 0 {
		t.Errorf("Extracted = %d, want 0 for empty-chunks file", st.Extracted)
	}
}

// TestPipelineHandleAllChunksFailedCounter covers the
// `writeErrors == len(chunks) → failed++` arm of handle. We
// close the underlying index between submit and processing so
// every IndexContent call fails with "indexer: closed". After
// the loop, writeErrors equals len(chunks), incrementing the
// Failed counter rather than the Extracted one.
func TestPipelineHandleAllChunksFailedCounter(t *testing.T) {
	t.Parallel()
	idx, err := Open(filepath.Join(t.TempDir(), "p_failed.bleve"))
	if err != nil {
		t.Fatal(err)
	}

	p := NewPipeline(idx, slog.New(slog.NewTextHandler(io.Discard, nil)), 0)
	p.Start()
	defer p.Stop()

	// Close the index now so IndexContent fails for every
	// submitted chunk. The pipeline will still extract text
	// from the input, but every Index call returns "closed".
	idx.Close()

	const ih = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	if !p.Submit(FileInput{
		InfoHash: ih,
		Path:     "blob.txt",
		Size:     32,
		OpenReader: func() (io.Reader, error) {
			return bytes.NewReader([]byte("the quick brown fox jumps over")), nil
		},
	}) {
		t.Fatal("Submit returned false")
	}
	pollPipelineProcessed(t, p, ih)
	st := p.Stats(ih)
	if st.Failed != 1 {
		t.Errorf("Failed = %d, want 1 when all chunks fail to index", st.Failed)
	}
	if st.Extracted != 0 {
		t.Errorf("Extracted = %d, want 0 when all chunks fail", st.Extracted)
	}
}

// closingReader wraps a bytes.Reader with a Close method so
// safeExtract's `r.(io.Closer); ok` type-assertion fires the
// defer-Close branch. The flag is atomic because the Close runs
// in safeExtract's extracting goroutine, concurrent with the
// asserting test goroutine.
type closingReader struct {
	*bytes.Reader
	closed atomic.Bool
}

func (c *closingReader) Close() error {
	c.closed.Store(true)
	return nil
}

// TestPipelineHandleClosesReader covers the
// `r.(io.Closer); ok → defer c.Close()` branch in safeExtract's
// extracting goroutine. Submit a .txt file whose OpenReader
// returns a *closingReader; assert that Close is (eventually)
// called on it. "Eventually" because the Close runs in the child
// goroutine's defer, which may fire just after handle returns.
func TestPipelineHandleClosesReader(t *testing.T) {
	t.Parallel()
	idx, err := Open(filepath.Join(t.TempDir(), "p3.bleve"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	p := NewPipeline(idx, slog.New(slog.NewTextHandler(io.Discard, nil)), 0)
	p.Start()
	defer p.Stop()

	const ih = "cccccccccccccccccccccccccccccccccccccccc"
	cr := &closingReader{Reader: bytes.NewReader([]byte("the quick brown fox jumps"))}
	if !p.Submit(FileInput{
		InfoHash:   ih,
		Path:       "blob.txt",
		Size:       int64(cr.Len()),
		OpenReader: func() (io.Reader, error) { return cr, nil },
	}) {
		t.Fatal("Submit returned false")
	}
	pollPipelineProcessed(t, p, ih)

	deadline := time.Now().Add(5 * time.Second)
	for !cr.closed.Load() {
		if time.Now().After(deadline) {
			t.Fatal("OpenReader's *closingReader.Close was never invoked")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
