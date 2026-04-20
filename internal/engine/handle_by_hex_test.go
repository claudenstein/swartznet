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

// TestPauseTorrentMalformedHex covers the FromHexString error
// branch of handleByHex. The existing tests pass valid hex
// representing an unknown infohash — they hit the !ok lookup
// branch but never the parse branch.
func TestPauseTorrentMalformedHex(t *testing.T) {
	t.Parallel()

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
	defer eng.Close()

	// 40 chars but not all hex digits.
	bad := strings.Repeat("z", 40)
	err = eng.PauseTorrent(bad)
	if err == nil {
		t.Fatal("PauseTorrent on malformed-hex infohash should error")
	}
	if !strings.Contains(err.Error(), "invalid infohash") {
		t.Errorf("error = %q, want it to mention 'invalid infohash'", err)
	}
}

// TestEngineCloseIdempotentReturnsCachedErr exercises the closed-
// already early return: a second Close call must short-circuit
// to e.closeErr without re-tearing the client. Coverage already
// has the first-Close path; this one pins the cached-error branch.
func TestEngineCloseIdempotentReturnsCachedErr(t *testing.T) {
	t.Parallel()

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

	first := eng.Close()
	second := eng.Close()
	// Both must agree: the cached closeErr is what every subsequent
	// Close returns.
	if (first == nil) != (second == nil) {
		t.Errorf("Close idempotency violated: first err = %v, second err = %v", first, second)
	}
}
