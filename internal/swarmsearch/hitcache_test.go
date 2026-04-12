package swarmsearch

import (
	"testing"
)

func TestHitCacheStoreLookup(t *testing.T) {
	t.Parallel()
	c := NewHitCache(10)

	hit := MergedHit{InfoHash: "aaa", Name: "ubuntu", Score: 100}
	c.Store(hit)

	got, ok := c.Lookup("aaa")
	if !ok {
		t.Fatal("cache miss for stored hit")
	}
	if got.Name != "ubuntu" || got.Score != 100 {
		t.Errorf("cached hit = %+v, want ubuntu/100", got)
	}

	_, ok = c.Lookup("bbb")
	if ok {
		t.Error("cache hit for unstored infohash")
	}
}

func TestHitCacheEviction(t *testing.T) {
	t.Parallel()
	c := NewHitCache(3) // tiny cache

	for i := 0; i < 5; i++ {
		c.Store(MergedHit{InfoHash: string(rune('a' + i))})
	}
	if c.Size() != 3 {
		t.Errorf("Size = %d, want 3 after eviction", c.Size())
	}
	// Oldest entries (a, b) should have been evicted.
	if _, ok := c.Lookup(string('a')); ok {
		t.Error("oldest entry 'a' should be evicted")
	}
	if _, ok := c.Lookup(string('b')); ok {
		t.Error("second-oldest entry 'b' should be evicted")
	}
	// Newest entries should still be present.
	if _, ok := c.Lookup(string('e')); !ok {
		t.Error("newest entry 'e' should be in cache")
	}
}

func TestHitCacheUpdateExisting(t *testing.T) {
	t.Parallel()
	c := NewHitCache(10)

	c.Store(MergedHit{InfoHash: "aaa", Name: "v1", Score: 50})
	c.Store(MergedHit{InfoHash: "aaa", Name: "v2", Score: 100})

	got, ok := c.Lookup("aaa")
	if !ok {
		t.Fatal("cache miss")
	}
	if got.Name != "v2" || got.Score != 100 {
		t.Errorf("updated hit = %+v, want v2/100", got)
	}
	if c.Size() != 1 {
		t.Errorf("Size = %d after update, want 1", c.Size())
	}
}

func TestHitCacheLRUOrder(t *testing.T) {
	t.Parallel()
	c := NewHitCache(3)

	c.Store(MergedHit{InfoHash: "a"})
	c.Store(MergedHit{InfoHash: "b"})
	c.Store(MergedHit{InfoHash: "c"})

	// Access 'a' to promote it to MRU.
	c.Lookup("a")

	// Insert 'd' — should evict 'b' (oldest), not 'a' (just promoted).
	c.Store(MergedHit{InfoHash: "d"})

	if _, ok := c.Lookup("b"); ok {
		t.Error("'b' should have been evicted as LRU")
	}
	if _, ok := c.Lookup("a"); !ok {
		t.Error("'a' should survive because it was promoted by Lookup")
	}
}
