package engine

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/contracts/ltepwire"
	"github.com/swartznet/swartznet/internal/config"
)

// capEngine builds an engine with default sharing (full local, file+content)
// and the given config mutation, for capability-mask tests.
func capEngine(t *testing.T, mutate func(*config.Config)) *Engine {
	t.Helper()
	cfg := config.Config{
		DataDir:          filepath.Join(t.TempDir(), "data"),
		ListenPort:       0,
		DisableDHT:       true,
		ShareLocal:       2,
		ShareFileHits:    true,
		ShareContentHits: true,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	e, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

// TestServicesMaskDefaultPublishing: a default node (index on, DHT publish on)
// advertises the full default set plus the Publisher bit → 0x2FD.
func TestServicesMaskDefaultPublishing(t *testing.T) {
	e := capEngine(t, nil)
	if got := e.ServicesMask(); got != 0x2FD {
		t.Errorf("default mask = 0x%x, want 0x2FD", got)
	}
	if !e.RuntimeFacts().Publishing {
		t.Error("default node should be Publishing")
	}
}

// TestNoIndexZeroesPublisherBit is the DoD --no-index cascade: bit 4 clears.
func TestNoIndexZeroesPublisherBit(t *testing.T) {
	e := capEngine(t, func(c *config.Config) { c.NoIndex = true })
	if e.RuntimeFacts().Publishing {
		t.Error("--no-index node must not be Publishing")
	}
	got := e.ServicesMask()
	if ltepwire.ServiceBits(got).Has(ltepwire.BitLayerDPublisher) {
		t.Errorf("--no-index mask 0x%x still has the Publisher bit", got)
	}
	if got != 0x2ED {
		t.Errorf("--no-index mask = 0x%x, want 0x2ED", got)
	}
}

// TestDisableDHTPublishZeroesPublisherBit: the other half of the gate.
func TestDisableDHTPublishZeroesPublisherBit(t *testing.T) {
	e := capEngine(t, func(c *config.Config) { c.DisableDHTPublish = true })
	if e.RuntimeFacts().Publishing {
		t.Error("--no-dht-publish node must not be Publishing")
	}
	if e.ServicesMask() != 0x2ED {
		t.Errorf("mask = 0x%x, want 0x2ED", e.ServicesMask())
	}
}

// TestRegtestSetsLoudBit: the rebuild realizes the "loud" regtest announce the
// legacy never actually set (bit 8).
func TestRegtestSetsLoudBit(t *testing.T) {
	e := capEngine(t, func(c *config.Config) { c.Regtest = true })
	if !e.RuntimeFacts().Regtest {
		t.Error("regtest node should report Regtest fact")
	}
	got := e.ServicesMask()
	if !ltepwire.ServiceBits(got).Has(ltepwire.BitRegtest) {
		t.Errorf("regtest mask 0x%x missing the loud bit 8", got)
	}
	// default publishing (0x2FD) + regtest (0x100) = 0x3FD.
	if got != 0x3FD {
		t.Errorf("regtest mask = 0x%x, want 0x3FD", got)
	}
}

// TestSetSharingDowngradeChangesMask: an operator downgrade clears bits 0..3
// live (the §6 defect-3 fix at the engine seam).
func TestSetSharingDowngradeChangesMask(t *testing.T) {
	e := capEngine(t, nil)
	if e.ServicesMask() != 0x2FD {
		t.Fatalf("pre-downgrade mask = 0x%x, want 0x2FD", e.ServicesMask())
	}
	e.SetSharing(ltepwire.Sharing{ShareLocal: 0, FileHits: false, ContentHits: false})
	// build bits (5,6,7,9 = 0x2E0) + Publisher (bit4 = 0x10) = 0x2F0.
	if got := e.ServicesMask(); got != 0x2F0 {
		t.Errorf("post-downgrade mask = 0x%x, want 0x2F0 (bits 0..3 cleared)", got)
	}
}

// TestSharingRoundTrip: SetSharing/Sharing preserves the prefs verbatim.
func TestSharingRoundTrip(t *testing.T) {
	e := capEngine(t, nil)
	want := ltepwire.Sharing{ShareLocal: 1, FileHits: true, ContentHits: false}
	e.SetSharing(want)
	if got := e.Sharing(); got != want {
		t.Errorf("Sharing round-trip = %+v, want %+v", got, want)
	}
}

// TestSharingSeededFromConfig: startup prefs come from config.
func TestSharingSeededFromConfig(t *testing.T) {
	e := capEngine(t, func(c *config.Config) {
		c.ShareLocal = 1
		c.ShareFileHits = false
		c.ShareContentHits = true
	})
	got := e.Sharing()
	if got.ShareLocal != 1 || got.FileHits || !got.ContentHits {
		t.Errorf("seeded Sharing = %+v, want {1,false,true}", got)
	}
}
