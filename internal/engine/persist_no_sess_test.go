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
// callers that might construct an engine without one.
//
// Each function tests `if e.sess == nil { return }` BEFORE touching
// the *Handle argument, so we can safely pass nil. This avoids
// AddMagnet/AddInfoHash, both of which spawn upgradeMagnetSession
// goroutines that race with the e.sess = nil write under -race.
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

	// Force-clear sess so the nil-sess guards short-circuit. No
	// goroutines reading e.sess exist yet because we haven't
	// added any magnets/infohashes.
	eng.mu.Lock()
	eng.sess = nil
	eng.mu.Unlock()

	// All four functions check `if e.sess == nil` first; passing
	// nil h is safe because they short-circuit before touching it.
	eng.persistAdd(nil, "magnet", "magnet:?xt=urn:btih:00", "")
	eng.persistState(nil)
	if err := eng.RestoreSession(); err != nil {
		t.Errorf("RestoreSession with nil sess = %v, want nil", err)
	}
	eng.upgradeMagnetSession(nil)
}
