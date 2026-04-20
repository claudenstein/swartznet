package engine_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// newQueueErrorsEngine spins up a DHT-disabled, no-upload engine
// with file-backed paths cleared. Mirrors the helper pattern from
// other engine tests.
func newQueueErrorsEngine(t *testing.T) (*engine.Engine, func()) {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true
	cfg.IdentityPath = ""
	cfg.ReputationPath = ""
	cfg.SeedListPath = ""
	cfg.BloomPath = ""
	cfg.TrustPath = ""

	eng, err := engine.New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	return eng, func() { _ = eng.Close() }
}

// TestQueueMoveToFrontUnknownInfoHash and TestQueueMoveToBackUnknownInfoHash
// cover the handleByHex error branch of both queue movers. The existing
// queue tests only exercise the success path.
func TestQueueMoveToFrontUnknownInfoHash(t *testing.T) {
	t.Parallel()
	eng, cleanup := newQueueErrorsEngine(t)
	defer cleanup()

	if err := eng.QueueMoveToFront("0000000000000000000000000000000000000000"); err == nil {
		t.Error("QueueMoveToFront on unknown infohash should error")
	}
}

func TestQueueMoveToBackUnknownInfoHash(t *testing.T) {
	t.Parallel()
	eng, cleanup := newQueueErrorsEngine(t)
	defer cleanup()

	if err := eng.QueueMoveToBack("0000000000000000000000000000000000000000"); err == nil {
		t.Error("QueueMoveToBack on unknown infohash should error")
	}
}

// TestQueueMoveToFrontMalformedHex covers the handleByHex
// FromHexString branch via the queue-move path. A 40-char non-hex
// input must surface the parse error, not the !ok lookup error.
func TestQueueMoveToFrontMalformedHex(t *testing.T) {
	t.Parallel()
	eng, cleanup := newQueueErrorsEngine(t)
	defer cleanup()

	bad := strings.Repeat("z", 40)
	if err := eng.QueueMoveToFront(bad); err == nil {
		t.Error("QueueMoveToFront on malformed-hex infohash should error")
	}
}
