package reputation_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/reputation"
)

// TestKnownGoodTestdataLoads loads the checked-in golden
// testdata/known-good.bloom — a frozen v1 file containing the three
// canonical infohashes — and asserts membership. This guards against
// any change to the on-disk format or hash derivation silently
// breaking compatibility with existing known-good.bloom files. The
// file is copied into a temp dir first so LoadOrCreateBloom cannot
// mutate the checked-in fixture.
func TestKnownGoodTestdataLoads(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("testdata", "known-good.bloom"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	path := filepath.Join(t.TempDir(), "known-good.bloom")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("stage fixture: %v", err)
	}

	bf, err := reputation.LoadOrCreateBloom(path)
	if err != nil {
		t.Fatalf("LoadOrCreateBloom: %v", err)
	}
	if bf.Bits() != 154 || bf.HashFunctions() != 7 {
		t.Errorf("params = m%d k%d, want m154 k7", bf.Bits(), bf.HashFunctions())
	}
	if bf.PopulationCount() != 17 {
		t.Errorf("PopulationCount = %d, want 17", bf.PopulationCount())
	}

	// The three seeded infohashes must all be present.
	ih3 := make([]byte, 20)
	for i := range ih3 {
		ih3[i] = byte(i)
	}
	members := [][]byte{
		bytes.Repeat([]byte{0x00}, 20),
		bytes.Repeat([]byte{0x01}, 20),
		ih3,
	}
	for i, ih := range members {
		if !bf.Test(ih) {
			t.Errorf("member %d absent from loaded fixture", i)
		}
	}

	// A never-added infohash should (with overwhelming probability)
	// miss this tiny, lightly-populated filter.
	if bf.Test(bytes.Repeat([]byte{0x42}, 20)) {
		t.Error("unexpected membership for never-added infohash 0x42*20")
	}
}
