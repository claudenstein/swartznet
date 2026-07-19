package reputation

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"math/bits"
	"os"
	"path/filepath"
	"sync"
)

// BloomFilter is a fixed-size Bloom filter optimised for the
// known-good-infohash use case. It is *not* a general-purpose Bloom
// implementation: 20-byte SHA-1 inputs only, FNV-derived hash
// functions, no resizing, persistent on disk in a tiny custom
// format.
//
// The defaults (1 million expected items, 0.01 false-positive rate)
// produce a ~1.2 MB filter. False positives slightly over-rank a
// "good" hit that isn't actually known good — harmless. False
// negatives don't exist because Bloom filters are one-sided.
type BloomFilter struct {
	mu   sync.RWMutex
	bits []uint64 // raw bitset, 64 bits per element
	m    uint64   // number of bits in the filter
	k    uint64   // number of hash functions

	path string // disk-persistence path; "" for memory-only
}

// BloomDefaultExpectedItems is the number of distinct infohashes
// the default filter is sized for. 1 million should be enough for
// any single user's downloaded torrents AND a year of confirmed
// hits combined.
const BloomDefaultExpectedItems = 1_000_000

// BloomDefaultFalsePositiveRate is the target false-positive rate
// of the default filter (0.01 = 1%). Combined with the expected
// item count above, this gives m ≈ 9.6 million bits ≈ 1.2 MB.
const BloomDefaultFalsePositiveRate = 0.01

// bloomFileMagic identifies a Bloom filter file on disk. The
// header is: magic[4] + version[2] + k[2] + m[8] + bitsLen[8],
// followed by the raw bitset. This on-disk format is a frozen
// golden format; existing known-good.bloom files must remain
// byte-compatible.
const bloomFileMagic = "SBLM" // "SwartzNet BLooM"
const bloomFileVersion uint16 = 1

// NewBloomFilter creates an empty in-memory Bloom filter sized for
// the given expected item count and target false-positive rate.
// Pass 0 for either argument to use the package defaults; fpRate
// outside (0,1) also falls back to the default rate.
func NewBloomFilter(expectedItems int, fpRate float64) *BloomFilter {
	if expectedItems <= 0 {
		expectedItems = BloomDefaultExpectedItems
	}
	if fpRate <= 0 || fpRate >= 1 {
		fpRate = BloomDefaultFalsePositiveRate
	}
	m, k := optimalSize(expectedItems, fpRate)
	return &BloomFilter{
		bits: make([]uint64, (m+63)/64),
		m:    m,
		k:    k,
	}
}

// LoadOrCreateBloom opens an existing Bloom filter at path or
// creates a fresh one with default parameters if the file is
// absent. Errors only on I/O or on a header that fails the
// fail-closed parse guards. Empty path is rejected.
func LoadOrCreateBloom(path string) (*BloomFilter, error) {
	if path == "" {
		return nil, errors.New("reputation: empty bloom path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("reputation: mkdir bloom dir: %w", err)
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			bf := NewBloomFilter(0, 0)
			bf.path = path
			return bf, nil
		}
		return nil, fmt.Errorf("reputation: open bloom: %w", err)
	}
	defer f.Close()
	bf, err := readBloom(f)
	if err != nil {
		return nil, err
	}
	bf.path = path
	return bf, nil
}

// Save persists the filter atomically (tempfile + rename).
// No-op for in-memory filters with empty path.
func (b *BloomFilter) Save() error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.path == "" {
		return nil
	}
	// A UNIQUE tempfile (not a fixed "<path>.tmp") so concurrent Save calls —
	// e.g. the periodic checkpoint racing a user confirm/flag — never share a
	// tmp inode and tear each other's writes into a corrupt file.
	f, err := os.CreateTemp(filepath.Dir(b.path), filepath.Base(b.path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("reputation: write bloom: %w", err)
	}
	tmp := f.Name()
	if err := writeBloom(f, b); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, b.path); err != nil {
		// Clean up so repeated rename failures don't leave a
		// growing collection of *.tmp files next to the filter.
		os.Remove(tmp)
		return err
	}
	return nil
}

// Add records the given infohash as known-good. Subsequent Test
// calls with the same infohash will always return true. Idempotent.
func (b *BloomFilter) Add(infohash []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, idx := range b.indices(infohash) {
		b.bits[idx/64] |= 1 << (idx % 64)
	}
}

// Test reports whether the infohash is "known-good" (probably).
// True is "probably yes" (subject to the configured FP rate),
// false is "definitely no". One-sided: no false negatives.
func (b *BloomFilter) Test(infohash []byte) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, idx := range b.indices(infohash) {
		if b.bits[idx/64]&(1<<(idx%64)) == 0 {
			return false
		}
	}
	return true
}

// PopulationCount returns the number of bits set in the filter.
// Useful for diagnostics — a filter with very high population is
// approaching its design false-positive rate.
func (b *BloomFilter) PopulationCount() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var n uint64
	for _, w := range b.bits {
		n += uint64(bits.OnesCount64(w))
	}
	return n
}

// EstimatedItems is a back-of-envelope estimate of how many distinct
// items have been added to the filter, derived from the population
// count. Accurate when the filter is below ~50% saturation; returns
// +Inf once every bit is set.
func (b *BloomFilter) EstimatedItems() float64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	m := float64(b.m)
	k := float64(b.k)
	var pop uint64
	for _, w := range b.bits {
		pop += uint64(bits.OnesCount64(w))
	}
	x := float64(pop)
	if x == 0 {
		// -m/k * log(1) computes to negative zero; report a clean 0.
		return 0
	}
	if x >= m {
		return math.Inf(1)
	}
	return -m / k * math.Log(1-x/m)
}

// Bits returns the configured bit-array size.
func (b *BloomFilter) Bits() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.m
}

// HashFunctions returns the number of hash functions.
func (b *BloomFilter) HashFunctions() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.k
}

// indices returns the k bit positions for the given input. This is
// a frozen derivation and must not change: two FNV-64a hashes are
// combined via the Kirsch-Mitzenmacher double-hashing trick as
// h1 + i*h2. h1 is FNV-64a(input); h2 continues the same hash state
// with one appended 0xff byte, then has its low bit forced set so
// the stride is odd (guaranteeing distinct indices rather than a
// collapse onto h1 % m). Any change here breaks byte-compatibility
// with existing known-good.bloom files.
func (b *BloomFilter) indices(input []byte) []uint64 {
	h := fnv.New64a()
	h.Write(input)
	h1 := h.Sum64()
	h.Write([]byte{0xff})
	h2 := h.Sum64()
	h2 |= 1

	out := make([]uint64, b.k)
	for i := uint64(0); i < b.k; i++ {
		out[i] = (h1 + i*h2) % b.m
	}
	return out
}

// optimalSize computes the bit-array length m and hash-function
// count k for the given expected item count n and target false-
// positive rate p, per the standard Bloom filter formulas:
//
//	m = -n * ln(p) / (ln(2)^2)
//	k = (m / n) * ln(2)
//
// Both are rounded up (math.Ceil) and floored to 1. k is derived
// from the pre-ceil float mF, not the ceiled m; this affects the
// default k and is part of the frozen sizing contract.
func optimalSize(n int, p float64) (m, k uint64) {
	mF := -float64(n) * math.Log(p) / (math.Ln2 * math.Ln2)
	kF := mF / float64(n) * math.Ln2
	m = uint64(math.Ceil(mF))
	if m == 0 {
		m = 1
	}
	k = uint64(math.Ceil(kF))
	if k == 0 {
		k = 1
	}
	return
}

// readBloom decodes a BloomFilter from r per the on-disk format.
// Parse guards fail closed: a corrupt header (bad magic, wrong
// version, m==0 or k==0, or a bitsLen that disagrees with m) is
// rejected here rather than panicking on the first Add/Test.
func readBloom(r io.Reader) (*BloomFilter, error) {
	var hdr [4 + 2 + 2 + 8 + 8]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, fmt.Errorf("reputation: read bloom header: %w", err)
	}
	if string(hdr[0:4]) != bloomFileMagic {
		return nil, fmt.Errorf("reputation: bad magic %q", hdr[0:4])
	}
	version := binary.LittleEndian.Uint16(hdr[4:6])
	if version != bloomFileVersion {
		return nil, fmt.Errorf("reputation: bloom version %d not supported", version)
	}
	k := uint64(binary.LittleEndian.Uint16(hdr[6:8]))
	m := binary.LittleEndian.Uint64(hdr[8:16])
	bitsLen := binary.LittleEndian.Uint64(hdr[16:24])
	// m and k must be positive: indices() divides by m and loops k
	// times, so a corrupt/truncated header with m=0 (or k=0) would
	// otherwise panic with a divide-by-zero on the next Test/Add.
	if m == 0 || k == 0 {
		return nil, fmt.Errorf("reputation: invalid bloom params m=%d k=%d", m, k)
	}
	// The writer always emits exactly (m+63)/64 words. Anything else —
	// undersized (truncated/corrupt file, would panic out-of-bounds on
	// the first Add/Test) or oversized — is rejected at parse time.
	if bitsLen != (m+63)/64 {
		return nil, fmt.Errorf("reputation: bitsLen %d inconsistent with m %d", bitsLen, m)
	}
	bits := make([]uint64, bitsLen)
	buf := make([]byte, 8)
	for i := range bits {
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, fmt.Errorf("reputation: read bloom bits: %w", err)
		}
		bits[i] = binary.LittleEndian.Uint64(buf)
	}
	return &BloomFilter{bits: bits, m: m, k: k}, nil
}

// writeBloom encodes a BloomFilter to w per the on-disk format.
// The caller already holds b.mu.RLock(). k must fit in a u16.
func writeBloom(w io.Writer, b *BloomFilter) error {
	var hdr [4 + 2 + 2 + 8 + 8]byte
	copy(hdr[0:4], bloomFileMagic)
	binary.LittleEndian.PutUint16(hdr[4:6], bloomFileVersion)
	if b.k > math.MaxUint16 {
		return fmt.Errorf("reputation: k=%d does not fit u16", b.k)
	}
	binary.LittleEndian.PutUint16(hdr[6:8], uint16(b.k))
	binary.LittleEndian.PutUint64(hdr[8:16], b.m)
	binary.LittleEndian.PutUint64(hdr[16:24], uint64(len(b.bits)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	buf := make([]byte, 8)
	for _, x := range b.bits {
		binary.LittleEndian.PutUint64(buf, x)
		if _, err := w.Write(buf); err != nil {
			return err
		}
	}
	return nil
}
