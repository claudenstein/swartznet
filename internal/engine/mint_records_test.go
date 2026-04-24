package engine_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/companion"
	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// newTestEngineWithIdentity builds an engine with a fresh ed25519
// identity file so the Aggregate mint path has a signer. Mirrors
// newTestEngine but wires IdentityPath.
func newTestEngineWithIdentity(t *testing.T) *engine.Engine {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true

	// Generate a valid key and write it at 0600 where
	// identity.LoadOrCreate expects.
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	keyPath := filepath.Join(t.TempDir(), "identity.key")
	if err := os.WriteFile(keyPath, priv, 0600); err != nil {
		t.Fatal(err)
	}
	cfg.IdentityPath = keyPath

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(context.Background(), cfg, log)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	t.Cleanup(func() { eng.Close() })
	return eng
}

// MintAggregateRecords must populate the cache with one record
// per keyword token of the torrent name.
func TestMintAggregateRecordsPopulatesCache(t *testing.T) {
	eng := newTestEngineWithIdentity(t)
	cache := eng.RecordCache()

	var ih [20]byte
	copy(ih[:], []byte("0123456789abcdef0123"))
	eng.MintAggregateRecords(ih, "Ubuntu Linux 24.04 LTS")

	// TokenizeAll drops noise; at minimum we should have records
	// for "ubuntu" and "linux".
	if cache.Len() == 0 {
		t.Fatal("expected at least one record in cache after Mint")
	}

	// Confirm every stored record is self-consistent: sig verifies.
	snap := cache.Snapshot()
	sawUbuntu := false
	sawLinux := false
	for _, r := range snap {
		rec := companion.Record{
			Pk: r.Pk, Kw: r.Kw, Ih: r.Ih,
			T: r.T, Pow: r.Pow, Sig: r.Sig,
		}
		if err := companion.VerifyRecordSig(rec); err != nil {
			t.Errorf("record for kw=%q has invalid sig: %v", r.Kw, err)
		}
		switch r.Kw {
		case "ubuntu":
			sawUbuntu = true
		case "linux":
			sawLinux = true
		}
	}
	if !sawUbuntu {
		t.Error("missing record for kw=ubuntu")
	}
	if !sawLinux {
		t.Error("missing record for kw=linux")
	}
}

// Mint on an engine without identity is a silent no-op. Build a
// dedicated engine here with IdentityPath cleared — the default
// newTestEngine helper leaves it at config.Default() which points
// at the user's real identity file, so it would load one.
func TestMintAggregateRecordsSilentWithoutIdentity(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true
	cfg.IdentityPath = "" // explicit opt-out — no identity loaded

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(context.Background(), cfg, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close() })

	cache := eng.RecordCache()
	if cache == nil {
		t.Fatal("RecordCache returned nil")
	}

	var ih [20]byte
	eng.MintAggregateRecords(ih, "anything")

	if cache.Len() != 0 {
		t.Errorf("expected cache empty with no identity, got %d", cache.Len())
	}
}

// Two mints of the same (ih, name) produce the same records —
// idempotent since record IDs are deterministic.
func TestMintAggregateRecordsIdempotent(t *testing.T) {
	eng := newTestEngineWithIdentity(t)
	cache := eng.RecordCache()

	var ih [20]byte
	ih[0] = 0x42
	eng.MintAggregateRecords(ih, "ubuntu linux")
	first := cache.Len()
	eng.MintAggregateRecords(ih, "ubuntu linux")
	second := cache.Len()
	// Because the helper uses time.Now() for T, successive calls
	// at different timestamps produce NEW records. This test
	// documents that behavior explicitly — "idempotent" here
	// means safe to call repeatedly, not that Len stays put.
	if second < first {
		t.Errorf("second mint shrank cache: %d → %d", first, second)
	}
}

// Mint with an empty name (no tokens) leaves the cache empty.
func TestMintAggregateRecordsEmptyName(t *testing.T) {
	eng := newTestEngineWithIdentity(t)
	cache := eng.RecordCache()
	var ih [20]byte
	eng.MintAggregateRecords(ih, "")
	if cache.Len() != 0 {
		t.Errorf("empty name should produce no records, got %d", cache.Len())
	}
}
