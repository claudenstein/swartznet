package reputation

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
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
// followed by the raw bitset.
const bloomFileMagic = "SBLM" // "SwartzNet BLooM"
const bloomFileVersion uint16 = 1

// NewBloomFilter creates an empty in-memory Bloom filter sized for
// the given expected item count and target false-positive rate.
// Pass 0 for either argument to use the package defaults.
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
// absent. Errors only on I/O or version mismatch.
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
	tmp := b.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("reputation: write bloom: %w", err)
	}
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
// calls with the same infohash will always return true.
func (b *BloomFilter) Add(infohash []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, idx := range b.indices(infohash) {
		b.bits[idx/64] |= 1 << (idx % 64)
	}
}

// Test reports whether the infohash is "known-good" (probably).
// True is "probably yes" (subject to the configured FP rate),
// false is "definitely no".
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
		n += uint64(popcountUint64(w))
	}
	return n
}

// EstimatedItems is a back-of-envelope estimate of how many distinct
// items have been added to the filter, derived from the population
// count. Accurate when the filter is below ~50% saturation.
func (b *BloomFilter) EstimatedItems() float64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	m := float64(b.m)
	k := float64(b.k)
	var pop uint64
	for _, w := range b.bits {
		pop += uint64(popcountUint64(w))
	}
	x := float64(pop)
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

// indices returns the k bit positions for the given input. Uses
// the well-known double-hashing trick: instead of running k
// independent hash functions, we compute two FNV hashes and
// combine them as h1 + i*h2 to derive k indices. Empirically this
// gives Bloom-filter performance indistinguishable from k true
// hashes.
func (b *BloomFilter) indices(input []byte) []uint64 {
	h := fnv.New64a()
	h.Write(input)
	h1 := h.Sum64()
	h.Write([]byte{0xff})
	h2 := h.Sum64()

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

// popcountUint64 returns the number of set bits in x. Plain
// software implementation; the compiler emits POPCNT on amd64.
func popcountUint64(x uint64) int {
	x = x - ((x >> 1) & 0x5555555555555555)
	x = (x & 0x3333333333333333) + ((x >> 2) & 0x3333333333333333)
	x = (x + (x >> 4)) & 0x0f0f0f0f0f0f0f0f
	return int((x * 0x0101010101010101) >> 56)
}

// readBloom decodes a BloomFilter from r per the on-disk format.
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
	if bitsLen > (m+63)/64+1 {
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
// The caller already holds b.mu.RLock().
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
