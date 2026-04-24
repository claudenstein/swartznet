package engine_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// TestEngineRecordCachePruneGoroutine verifies that engine.New
// launches a background goroutine that periodically calls
// PruneOlderThan on the RecordCache. In regtest mode the
// goroutine ticks every 200ms with a 500ms max-age, so by
// waiting ~1s we can observe old records (T=1) being evicted
// while recent records (T=now+1h) survive.
func TestEngineRecordCachePruneGoroutine(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true
	cfg.Regtest = true // triggers the 200ms/500ms prune cadence

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(context.Background(), cfg, log)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	t.Cleanup(func() { eng.Close() })

	cache := eng.RecordCache()
	if cache == nil {
		t.Fatal("engine.RecordCache() is nil")
	}

	// Old record — T=1 is far enough back (1970) that any
	// reasonable cutoff will evict it.
	var old swarmsearch.LocalRecord
	old.Pk[0] = 0xA1
	old.Kw = "ancient"
	old.Ih[0] = 0x01
	old.T = 1
	cache.Add(old)

	// Fresh record — T=now+1h is far enough in the future that
	// the goroutine's cutoff (now - 500ms) won't touch it during
	// the test window.
	var fresh swarmsearch.LocalRecord
	fresh.Pk[0] = 0xA2
	fresh.Kw = "fresh"
	fresh.Ih[0] = 0x02
	fresh.T = time.Now().Unix() + 3600

	cache.Add(fresh)

	if cache.Len() != 2 {
		t.Fatalf("pre-wait Len = %d, want 2", cache.Len())
	}

	src := eng.SwarmSearch().RecordSource()
	has := func(kw string) bool {
		recs, err := src.LocalRecords(swarmsearch.SyncFilter{})
		if err != nil {
			t.Fatalf("LocalRecords: %v", err)
		}
		for _, r := range recs {
			if r.Kw == kw {
				return true
			}
		}
		return false
	}

	// Wait up to 3s for the old record to be pruned. In regtest
	// mode the ticker fires every 200ms, so this should resolve
	// within ~1s on any non-starved CI.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !has("ancient") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if has("ancient") {
		t.Errorf("old record (T=1) was not pruned after 3s — goroutine may not be running")
	}
	if !has("fresh") {
		t.Error("fresh record (T=now+1h) should not have been pruned")
	}
}
