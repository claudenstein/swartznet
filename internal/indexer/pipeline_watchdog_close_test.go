package indexer

import (
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/indexer/extractors"
)

// trackedReadCloser is a reader whose Close sets an atomic flag, so a
// test can observe exactly when (and from which path) the pipeline
// closes the file reader. The flag is atomic because the Close runs in
// safeExtract's extracting goroutine.
type trackedReadCloser struct {
	io.Reader
	closed atomic.Bool
}

func (c *trackedReadCloser) Close() error {
	c.closed.Store(true)
	return nil
}

// gatedExtractor blocks inside Extract (holding the reader "in use")
// until its release channel is closed.
type gatedExtractor struct {
	release chan struct{}
}

func (gatedExtractor) Name() string { return "gated-test-extractor" }
func (e gatedExtractor) Extract(_ io.Reader, _ int64) ([]extractors.Chunk, error) {
	<-e.release
	return nil, nil
}

// pollClosed waits until c.closed flips true or fails the test.
func pollClosed(t *testing.T, c *trackedReadCloser, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !c.closed.Load() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: reader was never closed", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestHandleTimeoutDoesNotCloseReaderUnderExtract is the regression
// for the Read-after-Close race: when the extract watchdog fires,
// handle() must NOT close the file reader — the extractor goroutine is
// still using it, and in production it is an anacrolix torrent.Reader
// whose Read and Close are not safe to race. The Close must instead
// happen in the extracting goroutine, strictly after Extract returns.
//
// Before the fix, handle() held a `defer c.Close()` that fired on the
// timeout path while Extract was still blocked, so this test would
// observe closed==true immediately after handle returned.
func TestHandleTimeoutDoesNotCloseReaderUnderExtract(t *testing.T) {
	// Not parallel: mutates the package-level extractWatchdog.
	orig := extractWatchdog
	extractWatchdog = 20 * time.Millisecond
	defer func() { extractWatchdog = orig }()

	idx, err := Open(filepath.Join(t.TempDir(), "wd.bleve"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	p := NewPipeline(idx, slog.New(slog.NewTextHandler(io.Discard, nil)), 0)

	ex := gatedExtractor{release: make(chan struct{})}
	cr := &trackedReadCloser{Reader: strings.NewReader("payload")}

	// Drive safeExtract through the timeout path with the extractor
	// still blocked. handle() would go through dispatch (which knows
	// nothing about gatedExtractor), so call safeExtract directly the
	// way handle does — reader ownership is the seam under test.
	chunks, extractErr := safeExtract(p.log, ex, cr, p.maxFileBytes)
	if extractErr != errExtractTimeout {
		t.Fatalf("want errExtractTimeout, got chunks=%v err=%v", chunks, extractErr)
	}

	// The extractor is still blocked inside Extract: the reader must
	// still be open. The old code closed it here, under the extract.
	if cr.closed.Load() {
		t.Fatal("reader was closed while Extract was still running (Read-after-Close race)")
	}

	// Release the extractor; the child goroutine's defer must now
	// close the reader — serialized after Extract returned.
	close(ex.release)
	pollClosed(t, cr, "timeout path")
}

// TestSafeExtractClosesReaderOnNormalAndPanicPaths pins the other half
// of the ownership move: with the Close now living in safeExtract's
// extracting goroutine, the reader must still get closed on the
// ordinary success path and when the extractor panics (no fd leak).
func TestSafeExtractClosesReaderOnNormalAndPanicPaths(t *testing.T) {
	t.Parallel()

	// Success path.
	okReader := &trackedReadCloser{Reader: strings.NewReader("hello")}
	if _, err := safeExtract(nil, &happyExtractor{}, okReader, 1024); err != nil {
		t.Fatalf("happy extract: %v", err)
	}
	pollClosed(t, okReader, "success path")

	// Panic path.
	panicReader := &trackedReadCloser{Reader: strings.NewReader("payload bytes")}
	if _, err := safeExtract(nil, &panicExtractor{mode: "string"}, panicReader, 1024); err == nil {
		t.Fatal("expected error from panicking extractor")
	}
	pollClosed(t, panicReader, "panic path")
}
