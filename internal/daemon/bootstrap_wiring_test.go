package daemon_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/daemon"
)

// Bootstrap attaches when the engine produces a Lookup. With
// DisableDHT=true the engine has no Lookup so Bootstrap stays nil
// — covers the "DHT-off" daemon path.
func TestDaemonBootstrapNilWithoutDHT(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.IndexDir = t.TempDir()
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true
	cfg.IdentityPath = ""
	cfg.ReputationPath = ""
	cfg.SeedListPath = ""
	cfg.BloomPath = ""
	cfg.TrustPath = ""

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	d, err := daemon.New(context.Background(), daemon.Options{
		Cfg: cfg,
		Log: log,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	if d.Bootstrap != nil {
		t.Errorf("Bootstrap should be nil without a Lookup (DisableDHT=true), got %T", d.Bootstrap)
	}
}

// When DHT is enabled, the engine's Lookup exists so Bootstrap
// attaches. Run without hardcoded anchors so RunAnchors does
// nothing — the test only asserts the attachment, not the fetch.
func TestDaemonBootstrapAttachesWithDHT(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = dir
	cfg.IndexDir = t.TempDir()
	cfg.ListenPort = 0
	cfg.DisableDHT = false
	cfg.NoUpload = true
	// Hermetic XDG paths — but keep an Identity so the engine
	// constructs a Lookup (the publisher path requires an identity,
	// and Bootstrap requires a Lookup to attach to).
	cfg.IdentityPath = filepath.Join(dir, "identity.key")
	cfg.ReputationPath = ""
	cfg.SeedListPath = ""
	cfg.BloomPath = ""
	cfg.TrustPath = ""
	cfg.Regtest = true // use in-process DHT, don't hit mainline

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	d, err := daemon.New(context.Background(), daemon.Options{
		Cfg: cfg,
		Log: log,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	if d.Bootstrap == nil {
		t.Fatal("Bootstrap should attach when Lookup is available")
	}
	// Fresh bootstrap has the default anchor list (empty in dev
	// builds) and no candidates admitted yet.
	if got := d.Bootstrap.AdmittedCount(); got != 0 {
		t.Errorf("AdmittedCount = %d, want 0 on fresh bootstrap", got)
	}
}

// TestDaemonBootstrapWiredIntoAPI covers daemon.New's
// `if d.Bootstrap != nil { apiOpts.Bootstrap = d.Bootstrap }` arm
// that fires when both DHT is enabled (Bootstrap exists) AND an
// APIAddr is set so the HTTP server is constructed. Asserts that
// d.API is non-nil, which means apiOpts construction reached the
// Bootstrap-injection line.
func TestDaemonBootstrapWiredIntoAPI(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = dir
	cfg.IndexDir = t.TempDir()
	cfg.ListenPort = 0
	cfg.DisableDHT = false
	cfg.NoUpload = true
	cfg.IdentityPath = filepath.Join(dir, "identity.key")
	cfg.ReputationPath = ""
	cfg.SeedListPath = ""
	cfg.BloomPath = ""
	cfg.TrustPath = ""
	cfg.Regtest = true

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	d, err := daemon.New(context.Background(), daemon.Options{
		Cfg:     cfg,
		Log:     log,
		APIAddr: "127.0.0.1:0",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	if d.Bootstrap == nil {
		t.Fatal("Bootstrap should attach when DHT is enabled")
	}
	if d.API == nil {
		t.Error("API should attach when APIAddr is set")
	}
}
