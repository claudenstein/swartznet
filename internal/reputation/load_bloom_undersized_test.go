package reputation_test

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/reputation"
)

// bloomFileBytes hand-crafts a syntactically valid bloom file with
// the given k/m/bitsLen header fields and exactly bitsLen words of
// zeroed bitset, so the only thing under test is the header's
// internal consistency check.
func bloomFileBytes(k uint16, m, bitsLen uint64) []byte {
	hdr := make([]byte, 24)
	copy(hdr[0:4], "SBLM")
	binary.LittleEndian.PutUint16(hdr[4:6], 1) // version
	binary.LittleEndian.PutUint16(hdr[6:8], k)
	binary.LittleEndian.PutUint64(hdr[8:16], m)
	binary.LittleEndian.PutUint64(hdr[16:24], bitsLen)
	return append(hdr, make([]byte, bitsLen*8)...)
}

// TestLoadOrCreateBloomRejectsInconsistentBitsLen is the regression
// test for the one-sided bitsLen check: a file declaring a large m
// but a small (truncated) bitset used to load fine and then panic
// out-of-bounds on the first Add/Test. The loader must fail closed
// at parse time for both undersized and oversized bitsets, and for
// degenerate m/k values that would blow up later in indices().
func TestLoadOrCreateBloomRejectsInconsistentBitsLen(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		k       uint16
		m       uint64
		bitsLen uint64
	}{
		// m=9_585_059 is the default-parameter filter size; one
		// 64-bit word of bitset is the "truncated write" shape.
		{"undersized truncated bitset", 7, 9_585_059, 1},
		{"undersized by one word", 4, 128, 1},
		{"oversized by one word", 4, 64, 2},
		{"zero m", 4, 0, 0},
		{"zero k", 0, 64, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "bloom.bin")
			if err := os.WriteFile(path, bloomFileBytes(tc.k, tc.m, tc.bitsLen), 0o600); err != nil {
				t.Fatal(err)
			}
			bf, err := reputation.LoadOrCreateBloom(path)
			if err == nil {
				// Old behavior: load succeeded and the first
				// Add/Test panicked out-of-bounds. Demonstrate
				// the crash the guard prevents.
				bf.Add([]byte("12345678901234567890"))
				t.Fatal("LoadOrCreateBloom should reject inconsistent header at parse time")
			}
		})
	}
}

// TestLoadOrCreateBloomRoundTripStillWorks pins that the exact-match
// guard does not reject files the writer itself produces.
func TestLoadOrCreateBloomRoundTripStillWorks(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "bloom.bin")
	bf, err := reputation.LoadOrCreateBloom(path)
	if err != nil {
		t.Fatalf("LoadOrCreateBloom (create): %v", err)
	}
	ih := []byte("12345678901234567890")
	bf.Add(ih)
	if err := bf.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	bf2, err := reputation.LoadOrCreateBloom(path)
	if err != nil {
		t.Fatalf("LoadOrCreateBloom (reload): %v", err)
	}
	if !bf2.Test(ih) {
		t.Error("reloaded filter lost the added infohash")
	}
}
