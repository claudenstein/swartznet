package indexer

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/indexer/extractors"
)

// blockingExtractor blocks until its release channel is closed, so a
// test can hold the extract goroutine past the (shrunk) hard deadline
// deterministically — no time.Sleep races.
type blockingExtractor struct {
	release chan struct{}
}

func (blockingExtractor) Name() string { return "blocking-test-extractor" }
func (e blockingExtractor) Extract(_ io.Reader, _ int64) ([]extractors.Chunk, error) {
	<-e.release
	return []extractors.Chunk{{Text: "too late"}}, nil
}

// timeoutCounter counts pipeline.extract_timeout log records.
type timeoutCounter struct{ count *atomic.Int64 }

func (h timeoutCounter) Enabled(context.Context, slog.Level) bool { return true }
func (h timeoutCounter) Handle(_ context.Context, r slog.Record) error {
	if strings.HasPrefix(r.Message, "pipeline.extract_timeout") {
		h.count.Add(1)
	}
	return nil
}
func (h timeoutCounter) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h timeoutCounter) WithGroup(string) slog.Handler      { return h }

// TestSafeExtractHardDeadlineFires is the regression for the watchdog
// fix: a wedged extractor must NOT pin the worker. With a short
// deadline, safeExtract returns errExtractTimeout promptly while the
// extractor is still blocked, and logs pipeline.extract_timeout.
//
// Before the fix safeExtract called ex.Extract synchronously, so this
// test would block forever (caught by go test's package timeout).
func TestSafeExtractHardDeadlineFires(t *testing.T) {
	// Not parallel: mutates the package-level extractWatchdog.
	orig := extractWatchdog
	extractWatchdog = 20 * time.Millisecond
	defer func() { extractWatchdog = orig }()

	var seen atomic.Int64
	log := slog.New(timeoutCounter{count: &seen})

	ex := blockingExtractor{release: make(chan struct{})}
	defer close(ex.release) // unblock the leaked goroutine after the test

	start := time.Now()
	chunks, err := safeExtract(log, ex, strings.NewReader("payload"), 4096)
	elapsed := time.Since(start)

	if !errors.Is(err, errExtractTimeout) {
		t.Fatalf("want errExtractTimeout, got chunks=%v err=%v", chunks, err)
	}
	if chunks != nil {
		t.Fatalf("timed-out extract must yield nil chunks, got %v", chunks)
	}
	if elapsed > time.Second {
		t.Fatalf("safeExtract took %s — deadline did not interrupt the worker", elapsed)
	}
	if got := seen.Load(); got != 1 {
		t.Fatalf("expected exactly one extract_timeout log, got %d", got)
	}
}
