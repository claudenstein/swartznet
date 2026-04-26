package reputation

import (
	"errors"
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
