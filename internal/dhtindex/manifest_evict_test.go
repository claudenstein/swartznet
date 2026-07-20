package dhtindex

import (
	"bytes"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/contracts/dhtschema"
)

// TestManifestAddHitKeepsSingleOverCapHit is the regression for the empty-publish
// bug: a single hit whose cached name alone exceeds the BEP-44 value cap must be
// kept (with its name truncated to fit), never evicted to an empty list — which
// would publish a useless empty item and make the torrent undiscoverable.
func TestManifestAddHitKeepsSingleOverCapHit(t *testing.T) {
	m, _ := LoadOrCreateManifest("")
	ih := bytes.Repeat([]byte{0xab}, 20)
	hit := dhtschema.KeywordHit{IH: ih, N: strings.Repeat("x", 975)} // over the 1000-byte cap on its own

	n, err := m.AddHit("ubuntu", hit)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("AddHit kept %d hits, want 1 (must not evict the only hit to empty)", n)
	}
	entry := m.Entries["ubuntu"]
	if entry == nil || len(entry.Hits) != 1 {
		t.Fatalf("entry = %+v, want exactly 1 hit", entry)
	}
	// The stored value must fit under the cap so its DHT put is valid + non-empty.
	sz := dhtschema.EstimateValueSize(dhtschema.KeywordValue{Hits: entry.Hits})
	if sz > dhtschema.MaxValueBytes {
		t.Errorf("stored value is %d bytes, exceeds cap %d — name not truncated to fit", sz, dhtschema.MaxValueBytes)
	}
	if string(entry.Hits[0].IH) != string(ih) {
		t.Error("kept hit lost its infohash")
	}
}

// TestManifestAddHitStillEvictsOldestWhenMany keeps the ordinary multi-hit
// eviction working (drop oldest until it fits) after the single-hit guard.
func TestManifestAddHitStillEvictsOldestWhenMany(t *testing.T) {
	m, _ := LoadOrCreateManifest("")
	// Add many hits with moderate names so the value overflows and oldest evict.
	for i := 0; i < 40; i++ {
		ih := bytes.Repeat([]byte{byte(i)}, 20)
		if _, err := m.AddHit("linux", dhtschema.KeywordHit{IH: ih, N: strings.Repeat("n", 40)}); err != nil {
			t.Fatal(err)
		}
	}
	entry := m.Entries["linux"]
	if sz := dhtschema.EstimateValueSize(dhtschema.KeywordValue{Hits: entry.Hits}); sz > dhtschema.MaxValueBytes {
		t.Errorf("value %d bytes exceeds cap after eviction", sz)
	}
	if len(entry.Hits) == 40 {
		t.Error("no eviction happened despite overflow")
	}
	if len(entry.Hits) == 0 {
		t.Error("evicted everything")
	}
}
