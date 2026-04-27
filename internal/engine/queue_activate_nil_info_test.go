package engine

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
)

// TestActivateDownloadNilInfoArm covers activateDownload's
// `if h.T.Info() == nil { return }` early-return at queue.go:125-129.
// A fresh magnet handle has no metadata until GotInfo fires; with
// DHT disabled and no peers, that never happens. Calling
// activateDownload directly hits the nil-info arm.
func TestActivateDownloadNilInfoArm(t *testing.T) {
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

	const magnet = "magnet:?xt=urn:btih:abababababababababababababababababababab"
	h, err := eng.AddMagnet(magnet)
	if err != nil {
		t.Fatalf("AddMagnet: %v", err)
	}
	// Pre-mark queued so we can verify activateDownload flips it
	// off — the side-effect we use to confirm we entered the
	// function but took the nil-info early return.
	h.setQueued(true)
	activateDownload(h)
	if h.IsQueued() {
		t.Error("activateDownload should have unqueued the handle")
	}
	// And t.Files() must NOT have been touched (Info is nil) —
	// we just rely on no panic + the unqueue check.
}
