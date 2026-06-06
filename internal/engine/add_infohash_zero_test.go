package engine_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// TestAddInfoHashZeroRejected covers the zero-infohash guard added to
// AddInfoHash. A zero infohash reaches AddInfoHash from an untrusted
// BEP-46 content-index pointer (companion subscriber path); without
// the guard, anacrolix's AddTorrentInfoHashWithStorage runs
// panicif.Zero and crashes the daemon. The guard must reject it with
// a clean error and never panic.
func TestAddInfoHashZeroRejected(t *testing.T) {
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

	var zero [20]byte
	h, err := eng.AddInfoHash(zero)
	if err == nil {
		t.Fatal("AddInfoHash(zero) returned nil error; want rejection")
	}
	if h != nil {
		t.Errorf("AddInfoHash(zero) returned non-nil handle %v", h)
	}

	// A non-zero infohash must still be accepted (the guard is
	// surgical, not a blanket reject).
	var nonZero [20]byte
	for i := range nonZero {
		nonZero[i] = byte(i + 1)
	}
	if _, err := eng.AddInfoHash(nonZero); err != nil {
		t.Fatalf("AddInfoHash(nonZero): unexpected error %v", err)
	}
}
