package reputation

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"testing"
)

// failingWriter returns a synthetic error after writing N bytes.
// Used to drive writeBloom's two distinct Write-error arms — one
// at the header and one inside the bits-loop.
type failingWriter struct {
	allowedBytes int
	written      int
}

func (f *failingWriter) Write(p []byte) (int, error) {
	if f.written >= f.allowedBytes {
		return 0, errors.New("simulated write failure")
	}
	n := len(p)
	if f.written+n > f.allowedBytes {
		n = f.allowedBytes - f.written
	}
	f.written += n
	if n < len(p) {
		return n, errors.New("simulated short write")
	}
	return n, nil
}

// TestWriteBloomHeaderWriteError covers writeBloom's first
// `if _, err := w.Write(hdr[:]); err != nil { return err }`
// arm. The writer fails before accepting any bytes so the
// header write surfaces the error.
func TestWriteBloomHeaderWriteError(t *testing.T) {
	t.Parallel()
	bf := NewBloomFilter(16, 0.01)
	bf.Add([]byte("test"))
	w := &failingWriter{allowedBytes: 0}
	if err := writeBloom(w, bf); err == nil {
		t.Error("writeBloom should surface header-write error")
	}
}

// TestWriteBloomBitsWriteError covers writeBloom's second
// `if _, err := w.Write(buf); err != nil { return err }` arm
// inside the bits-loop. The writer accepts the 24-byte header
// but fails on the first bits-buffer write.
func TestWriteBloomBitsWriteError(t *testing.T) {
	t.Parallel()
	bf := NewBloomFilter(16, 0.01)
	bf.Add([]byte("test"))
	// Header is 24 bytes (4 magic + 2 version + 2 k + 8 m + 8 bitslen).
	w := &failingWriter{allowedBytes: 24}
	if err := writeBloom(w, bf); err == nil {
		t.Error("writeBloom should surface bits-loop write error")
	}
}

// TestEstimatedItemsSaturated covers EstimatedItems'
// `if x >= m { return math.Inf(1) }` arm. Saturate every
// bit in a small filter so popcount equals m.
func TestEstimatedItemsSaturated(t *testing.T) {
	t.Parallel()
	bf := NewBloomFilter(8, 0.5)
	// Set every bit by writing all-ones into the bits slice
	// directly. We're inside the package so this is allowed.
	for i := range bf.bits {
		bf.bits[i] = ^uint64(0)
	}
	got := bf.EstimatedItems()
	if !math.IsInf(got, 1) {
		t.Errorf("EstimatedItems on saturated filter = %v, want +Inf", got)
	}
}

// TestReadBloomBitsLenInconsistent covers readBloom's
// `if bitsLen != (m+63)/64` guard. Hand-craft a header whose
// declared bitsLen exceeds what m would allow, and verify
// readBloom rejects rather than allocating a huge slice.
func TestReadBloomBitsLenInconsistent(t *testing.T) {
	t.Parallel()
	hdr := make([]byte, 24)
	copy(hdr[0:4], bloomFileMagic)
	binary.LittleEndian.PutUint16(hdr[4:6], bloomFileVersion)
	binary.LittleEndian.PutUint16(hdr[6:8], 4)              // k=4
	binary.LittleEndian.PutUint64(hdr[8:16], 64)            // m=64 → expected bitsLen ~1
	binary.LittleEndian.PutUint64(hdr[16:24], 1_000_000_000) // huge claimed bitsLen
	if _, err := readBloom(bytes.NewReader(hdr)); err == nil {
		t.Error("readBloom should reject bitsLen inconsistent with m")
	}
}

// TestReadBloomBitsReadError covers readBloom's
// `io.ReadFull(r, buf)` error arm inside the bits-loop. We
// supply a header whose bitsLen claims 2 entries but the
// reader has only enough bytes for the header plus 1 entry.
// The 2nd ReadFull returns io.EOF / unexpected EOF.
func TestReadBloomBitsReadError(t *testing.T) {
	t.Parallel()
	// m=128 → expected bitsLen = (128+63)/64 = 2, so the header
	// passes the exact-match guard and the loop does the failing.
	hdr := make([]byte, 24)
	copy(hdr[0:4], bloomFileMagic)
	binary.LittleEndian.PutUint16(hdr[4:6], bloomFileVersion)
	binary.LittleEndian.PutUint16(hdr[6:8], 4)    // k=4
	binary.LittleEndian.PutUint64(hdr[8:16], 128) // m=128
	binary.LittleEndian.PutUint64(hdr[16:24], 2)  // bitsLen=2
	// Provide only 8 bytes of bits (= 1 entry); the second
	// ReadFull fails.
	body := append(hdr, make([]byte, 8)...)
	if _, err := readBloom(bytes.NewReader(body)); err == nil {
		t.Error("readBloom should fail when bits truncate mid-read")
	}
}
