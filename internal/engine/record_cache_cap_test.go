package engine_test

import (
	"testing"

	"github.com/swartznet/swartznet/internal/engine"
)

// engine.New must apply DefaultRecordCacheMax so daemons have
// a bounded memory footprint from the moment they start. Tests
// the cap value — not the eviction mechanism itself, that's
// covered in swarmsearch.
func TestEngineDefaultRecordCacheCap(t *testing.T) {
	eng := newTestEngine(t)
	cache := eng.RecordCache()
	if cache == nil {
		t.Fatal("nil RecordCache")
	}
	got := cache.MaxRecords()
	if got != engine.DefaultRecordCacheMax {
		t.Errorf("RecordCache MaxRecords = %d, want DefaultRecordCacheMax (%d)",
			got, engine.DefaultRecordCacheMax)
	}
	// Sanity: the default should be positive. Guards against a
	// future refactor accidentally setting the const to 0.
	if got <= 0 {
		t.Errorf("DefaultRecordCacheMax = %d should be positive", got)
	}
}
