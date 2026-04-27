package reputation

import (
	"path/filepath"
	"testing"
)

// TestBloomSaveWriteBloomKTooLarge covers the writeBloom-error
// arm of BloomFilter.Save: writeBloom rejects b.k > math.MaxUint16
// up front, so a hand-built Bloom with k = 70_000 trips that
// guard, propagating the error out of Save and exercising the
// `f.Close(); os.Remove(tmp); return err` cleanup path.
//
// Internal test (package reputation, not reputation_test) so we
// can construct a BloomFilter with bad-fit k directly.
func TestBloomSaveWriteBloomKTooLarge(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "bloom.bin")

	bf := &BloomFilter{
		bits: make([]uint64, 1),
		m:    64,
		k:    70_000, // > math.MaxUint16 — trips writeBloom's k-fits-u16 guard
		path: path,
	}

	if err := bf.Save(); err == nil {
		t.Error("Save should fail when k > MaxUint16 (writeBloom returns err)")
	}
}
