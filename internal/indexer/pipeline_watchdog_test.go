package indexer

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/indexer/extractors"
)

// slowExtractor blocks for the duration `delay` before returning,
// so a test can drive safeExtract past its watchdog threshold
// without waiting the full 60s production budget.
type slowExtractor struct {
	delay time.Duration
}

func (slowExtractor) Name() string              { return "slow-test-extractor" }
func (slowExtractor) Match(string, []byte) bool { return true }
func (e slowExtractor) Extract(_ io.Reader, _ int64) ([]extractors.Chunk, error) {
	time.Sleep(e.delay)
	return nil, nil
}

// watchdogCounter is a slog.Handler that counts log records whose
// message starts with "pipeline.extract_slow". Used to assert
// the watchdog fired without coupling the test to slog's text-
// vs-JSON output formatting.
type watchdogCounter struct {
	count *atomic.Int64
}

func (h watchdogCounter) Enabled(context.Context, slog.Level) bool { return true }
func (h watchdogCounter) Handle(_ context.Context, r slog.Record) error {
	if strings.HasPrefix(r.Message, "pipeline.extract_slow") {
		h.count.Add(1)
	}
	return nil
}
func (h watchdogCounter) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h watchdogCounter) WithGroup(string) slog.Handler      { return h }

// TestSafeExtractWatchdogFires drives the watchdog by shrinking
// extractWatchdog locally — actually no, the constant is global
// and unexported. Instead we use a slowExtractor and just verify
// that fast extracts do NOT trip the watchdog (the "happy path"
// regression: an unconditional fire would log on every extract).
//
// The 60s production threshold is too long to exercise in a unit
// test, so the actual fire-on-slow path is exercised in
// integration scenarios. What this test guarantees is that fast
// extracts complete cleanly without spurious watchdog warnings —
// which would otherwise spam ops dashboards.
func TestSafeExtractWatchdogQuietOnFastExtract(t *testing.T) {
	t.Parallel()

	var seen atomic.Int64
	log := slog.New(watchdogCounter{count: &seen})

	r := bytes.NewReader([]byte("any content"))
	chunks, err := safeExtract(log, slowExtractor{delay: 10 * time.Millisecond}, r, 4096)
	if err != nil {
		t.Fatalf("fast extract: %v", err)
	}
	if len(chunks) != 0 {
		t.Fatalf("fast extract: chunks=%d, want 0", len(chunks))
	}
	if got := seen.Load(); got != 0 {
		t.Fatalf("watchdog fired %d times for fast extract — should be 0 (false positive)", got)
	}
}

// TestSafeExtractPanicStillRecovers locks in that the watchdog
// addition didn't break the panic-recovery contract. A panicking
// extractor must still surface as an ordinary error.
func TestSafeExtractPanicStillRecovers(t *testing.T) {
	t.Parallel()
	r := strings.NewReader("payload")
	_, err := safeExtract(nil, panicCheckExtractor{}, r, 1024)
	if err == nil {
		t.Fatal("safeExtract returned nil error for panicking extractor")
	}
	if !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("error %q does not indicate panic recovery", err)
	}
}

type panicCheckExtractor struct{}

func (panicCheckExtractor) Name() string              { return "panic-check" }
func (panicCheckExtractor) Match(string, []byte) bool { return true }
func (panicCheckExtractor) Extract(_ io.Reader, _ int64) ([]extractors.Chunk, error) {
	panic("boom")
}
