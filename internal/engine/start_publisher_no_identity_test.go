package engine

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
)

// TestStartPublisherNoIdentity covers startPublisher's
// `if e.identity == nil → "no identity"` defensive arm at
// engine.go:896-898. New() only invokes startPublisher when
// identity loaded successfully, but the explicit nil-check
// future-proofs callers that might construct an engine without
// an identity. Reach the arm via the internal entry point.
func TestStartPublisherNoIdentity(t *testing.T) {
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

	eng, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	// Construct path: identity wasn't loaded (IdentityPath empty),
	// so e.identity is nil. The first guard in startPublisher must
	// surface "no identity" without touching the DHT.
	err = eng.startPublisher()
	if err == nil {
		t.Fatal("startPublisher with nil identity should error")
	}
	if !strings.Contains(err.Error(), "no identity") {
		t.Errorf("err = %q, want it to mention 'no identity'", err.Error())
	}
}
