package engine_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// TestDHTRoutingTableSizeNoDHTReturnsZero locks the contract
// that DHTRoutingTableSize returns (0, 0) when the engine ran
// with DisableDHT=true. Three surfaces read this value —
// Fyne GUI card, web UI card, CLI `swartznet status` text —
// and two of them use the value to decide whether to render
// the DHT block at all. If the accessor ever panicked or
// returned spurious non-zero values for a DHT-disabled
// engine, the UI would mis-render and operators would see a
// confusing "DHT routing" card pointing at nothing.
func TestDHTRoutingTableSizeNoDHTReturnsZero(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.IndexDir = t.TempDir()
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	// Neutralise every side-loaded file so the engine starts
	// clean and the test is hermetic.
	cfg.IdentityPath = ""
	cfg.ReputationPath = ""
	cfg.SeedListPath = ""
	cfg.BloomPath = ""
	cfg.TrustPath = ""
	cfg.PublisherManifest = ""
	cfg.CompanionDir = ""
	cfg.CompanionFollowFile = ""

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(context.Background(), cfg, log)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	good, total := eng.DHTRoutingTableSize()
	if good != 0 || total != 0 {
		t.Errorf("DHTRoutingTableSize() = (%d, %d), want (0, 0) for DisableDHT=true",
			good, total)
	}
}

// TestDHTRoutingTableSizeWithDHTReturnsNonNegative covers the
// opposite: with DHT enabled, the accessor returns a valid
// (good, total) pair where good <= total and both are >= 0.
// We don't assert specific values — a solo node hasn't pinged
// anyone yet so the counts are probably 0 — but the accessor
// must not crash, return negative values, or report good >
// total (which would break the GUI's "isolated state"
// heuristic of Total > 0 && Good == 0).
func TestDHTRoutingTableSizeWithDHTReturnsNonNegative(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.IndexDir = t.TempDir()
	cfg.ListenPort = 0
	cfg.DisableDHT = false
	cfg.DisableDHTPublish = true // we only probe the accessor, no publishing
	cfg.DHTInsecure = true
	cfg.ListenHost = "127.0.0.1"
	cfg.DisableIPv6 = true
	// Dead-end bootstrap so the engine doesn't reach the real
	// public mainline DHT during a unit test.
	cfg.DHTBootstrapAddrs = []string{"127.0.0.1:1"}
	cfg.IdentityPath = ""
	cfg.ReputationPath = ""
	cfg.SeedListPath = ""
	cfg.BloomPath = ""
	cfg.TrustPath = ""
	cfg.PublisherManifest = ""
	cfg.CompanionDir = ""
	cfg.CompanionFollowFile = ""

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(context.Background(), cfg, log)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	good, total := eng.DHTRoutingTableSize()
	if good < 0 || total < 0 {
		t.Errorf("DHTRoutingTableSize() = (%d, %d), counts must be non-negative",
			good, total)
	}
	if good > total {
		t.Errorf("DHTRoutingTableSize() good=%d > total=%d; invariant violated",
			good, total)
	}
}
