package engine_test

import (
	"testing"

	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// engine.New must construct and attach a RecordCache so every
// downstream consumer (sync responder, /aggregate endpoint,
// GUI/CLI status cards) sees a consistent non-nil source from
// the moment the daemon starts.
func TestEngineNewAttachesRecordCache(t *testing.T) {
	eng := newTestEngine(t)

	cache := eng.RecordCache()
	if cache == nil {
		t.Fatal("Engine.RecordCache() returned nil — wiring missing")
	}
	if cache.Len() != 0 {
		t.Errorf("fresh cache should be empty, has %d records", cache.Len())
	}

	swarm := eng.SwarmSearch()
	if swarm == nil {
		t.Fatal("Engine.SwarmSearch() returned nil")
	}
	src := swarm.RecordSource()
	if src == nil {
		t.Fatal("swarm.RecordSource() returned nil after Engine.New")
	}
	// The attached source MUST be the same instance the engine
	// exposes via RecordCache() — confirms the wiring routes
	// Add() calls into the live responder's read path.
	if src != cache {
		t.Error("swarm.RecordSource() is not the same instance as Engine.RecordCache()")
	}
	// Type-assert to verify it's our in-repo cache, not something else.
	if _, ok := src.(*swarmsearch.RecordCache); !ok {
		t.Errorf("RecordSource is %T, want *swarmsearch.RecordCache", src)
	}
}

// Adding a record via Engine.RecordCache() must make it visible
// to the swarm.RecordSource's LocalRecords response.
func TestEngineRecordCacheAddVisibleToSource(t *testing.T) {
	eng := newTestEngine(t)
	cache := eng.RecordCache()
	var r swarmsearch.LocalRecord
	r.Pk[0] = 0xAB
	r.Kw = "ubuntu"
	r.Ih[0] = 0x10
	r.T = 1
	cache.Add(r)

	src := eng.SwarmSearch().RecordSource()
	recs, err := src.LocalRecords(swarmsearch.SyncFilter{})
	if err != nil {
		t.Fatalf("LocalRecords: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("len(recs) = %d, want 1", len(recs))
	}
	if recs[0].Kw != "ubuntu" {
		t.Errorf("kw = %q, want ubuntu", recs[0].Kw)
	}
}
