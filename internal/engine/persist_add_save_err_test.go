package engine_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// TestPersistAddTolerateSaveError covers persistAdd's
// `e.sess.update(...) err != nil → log.Warn(...)` arm. Make the
// engine's DataDir read-only after engine.New so the saveLocked
// inside session.update fails. AddMagnet must still succeed
// (persist failures are non-fatal).
//
// Skipped on Windows and as root.
func TestPersistAddTolerateSaveError(t *testing.T) {
	t.Parallel()
	if os.Getuid() == 0 {
		t.Skip("running as root, chmod 0o500 doesn't deny writes")
	}
	dataDir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = dataDir
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

	// Strip write permission on DataDir so saveLocked's tmp
	// WriteFile fails when persistAdd → session.update fires.
	if err := os.Chmod(dataDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dataDir, 0o755) })

	var ih [20]byte
	if _, err := rand.Read(ih[:]); err != nil {
		t.Fatal(err)
	}
	hexIH := hex.EncodeToString(ih[:])
	if _, err := eng.AddMagnet("magnet:?xt=urn:btih:" + hexIH); err != nil {
		t.Fatalf("AddMagnet: %v (persistAdd save failure must be non-fatal)", err)
	}
}
