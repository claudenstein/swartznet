package swarmsearch

import (
	"testing"
)

// BenchmarkRecordCacheAddUnbounded is the baseline: unlimited
// cap, no eviction pressure. Measures raw map-write + ID-hash
// cost per Add.
func BenchmarkRecordCacheAddUnbounded(b *testing.B) {
	c := NewRecordCache()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c.Add(mkRec("k", byte(i&0xFF), int64(i)))
	}
}

// BenchmarkRecordCacheAddUnderCap exercises the eviction hot
// path: every Add after iteration `cap` drops one record and
// writes one. Measures the steady-state cost of a saturated
// bounded cache.
func BenchmarkRecordCacheAddUnderCap(b *testing.B) {
	const cap = 1000
	c := NewRecordCache()
	c.SetMaxRecords(cap)

	// Pre-warm the cache to cap so every timed Add triggers an
	// eviction.
	for i := 0; i < cap; i++ {
		c.Add(mkRec("k", byte(i&0xFF), int64(i)))
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c.Add(mkRec("k", byte(i&0xFF), int64(i+cap)))
	}
}

// BenchmarkRecordCachePruneOlderThan measures the cost of
// scanning the whole cache to drop time-expired records. Cap
// determines the scan length.
func BenchmarkRecordCachePruneOlderThan(b *testing.B) {
	const n = 10_000
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		c := NewRecordCache()
		for j := 0; j < n; j++ {
			c.Add(mkRec("k", byte(j&0xFF), int64(j)))
		}
		b.StartTimer()
		// Drop the oldest half.
		_ = c.PruneOlderThan(int64(n / 2))
	}
}

// TestRecordCacheEvictionUnderLoad pumps 200k distinct records
// through a cache capped at 1 000. Asserts final Len stays at
// the cap — no leaks, no unexpected growth.
func TestRecordCacheEvictionUnderLoad(t *testing.T) {
	const cap = 1000
	const total = 200_000

	c := NewRecordCache()
	c.SetMaxRecords(cap)

	for i := 0; i < total; i++ {
		// Use a 3-byte varying IH prefix so every Add gets a
		// distinct ID. (Byte-based 0..255 alone wraps; we vary
		// Ih[0],Ih[1],Ih[2],T together so 200k IDs stay unique.)
		var r LocalRecord
		r.Pk[0] = 0x11
		r.Kw = "k"
		r.Ih[0] = byte(i)
		r.Ih[1] = byte(i >> 8)
		r.Ih[2] = byte(i >> 16)
		r.T = int64(i)
		c.Add(r)
	}

	if c.Len() != cap {
		t.Errorf("Len = %d after %d Adds against cap %d, want %d",
			c.Len(), total, cap, cap)
	}
	// The surviving records should be the newest batch — T
	// values in [total-cap, total). Spot-check one.
	var probe LocalRecord
	probe.Pk[0] = 0x11
	probe.Kw = "k"
	const last = total - 1
	probe.Ih[0] = byte(last & 0xFF)
	probe.Ih[1] = byte((last >> 8) & 0xFF)
	probe.Ih[2] = byte((last >> 16) & 0xFF)
	probe.T = int64(total - 1)
	if _, ok := c.Get(cacheRecordID(probe)); !ok {
		t.Error("newest record missing after load test")
	}

	// Verify the oldest record is gone.
	var oldest LocalRecord
	oldest.Pk[0] = 0x11
	oldest.Kw = "k"
	// T=0 record
	if _, ok := c.Get(cacheRecordID(oldest)); ok {
		t.Error("T=0 record should have been evicted long ago")
	}
}

// Sanity: PruneOlderThan reclaims the expected number of entries
// under a realistic size. Separate test so it runs at low t
// without benchmark overhead.
func TestRecordCachePruneOlderThanMidSize(t *testing.T) {
	c := NewRecordCache()
	for i := 0; i < 1000; i++ {
		c.Add(mkRec("k", byte(i&0xFF), int64(i)))
	}
	dropped := c.PruneOlderThan(500)
	if dropped < 500 || dropped > 600 {
		t.Errorf("dropped %d records (T<500), expected around 500", dropped)
	}
	// Sanity: byte(i&0xFF) causes IH collisions every 256
	// increments, so T values aren't quite 1000 distinct — but
	// the cut at T=500 should still halve the set roughly. If
	// we got < 500 something in prune scan is broken.
}
