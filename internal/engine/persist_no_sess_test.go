package engine

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
)

// TestPersistFnsNoSessReturnEarly covers the nil-sess defensive
// arm at the top of persistAdd, persistState, upgradeMagnetSession,
// and RestoreSession (engine.go:1325, 1363, 1407, 1460). New()
// always populates e.sess, but the explicit guards future-proof
// callers that might construct an engine without one. Reach the
// arms via the internal entry points after force-clearing e.sess.
func TestPersistFnsNoSessReturnEarly(t *testing.T) {
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

	const magnet = "magnet:?xt=urn:btih:1111111111111111111111111111111111111111"
	h, err := eng.AddMagnet(magnet)
	if err != nil {
		t.Fatalf("AddMagnet: %v", err)
	}

	// Force-clear sess so the nil-sess guards short-circuit.
	eng.sess = nil

	// persistAdd: void return; success means it didn't panic.
	eng.persistAdd(h, "magnet", magnet, "")

	// persistState: same.
	eng.persistState(h)

	// RestoreSession: returns nil on nil-sess.
	if err := eng.RestoreSession(); err != nil {
		t.Errorf("RestoreSession with nil sess = %v, want nil", err)
	}

	// upgradeMagnetSession is a goroutine entrypoint that returns
	// immediately on nil-sess. Calling synchronously is safe.
	eng.upgradeMagnetSession(h)
}
