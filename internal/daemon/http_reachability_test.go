package daemon_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/daemon"
)

// TestHTTPAPIStaysReachable asserts that after daemon.New returns, the
// HTTP API listener actually accepts connections and keeps accepting
// them for at least a few seconds of idle lifetime. An earlier manual
// probe showed the listener disappearing shortly after the
// "httpapi.listening" log line — this test guards against that.
func TestHTTPAPIStaysReachable(t *testing.T) {
	t.Parallel()

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
		Cfg:     cfg,
		Log:     log,
		APIAddr: "127.0.0.1:0",
		Version: "test",
	})
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	if d.API == nil {
		t.Fatal("d.API is nil despite APIAddr set")
	}
	base := "http://" + d.API.Addr()

	client := &http.Client{Timeout: 2 * time.Second}

	// First probe: immediately after New returns.
	resp, err := client.Get(base + "/healthz")
	if err != nil {
		t.Fatalf("first /healthz GET failed: %v (Addr=%s)", err, d.API.Addr())
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first /healthz status = %d, want 200", resp.StatusCode)
	}

	// Wait past any potential short-lived-server race.
	time.Sleep(2 * time.Second)

	// Second probe: after a grace period.
	resp, err = client.Get(base + "/healthz")
	if err != nil {
		t.Fatalf("second /healthz GET failed: %v (Addr=%s)", err, d.API.Addr())
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second /healthz status = %d, want 200", resp.StatusCode)
	}

	// Status endpoint should also respond.
	resp, err = client.Get(base + "/status")
	if err != nil {
		t.Fatalf("/status GET failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/status status = %d, want 200", resp.StatusCode)
	}
}
