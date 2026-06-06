package extractors

import (
	"bytes"
	"testing"
)

// TestID3TagSizeClampedToBudget is the regression for the id3 fix:
// a header declaring a near-maximum syncsafe tag size (~256 MiB) must
// not drive a make([]byte, 10+tagSize) allocation when the actual
// input — and the maxBytes budget — are tiny. We pass a small maxBytes;
// the clamp limits the allocation to the budget, and io.ReadFull then
// reports the truncated read as an error (no OOM, no giant alloc).
func TestID3TagSizeClampedToBudget(t *testing.T) {
	t.Parallel()

	var hdr bytes.Buffer
	hdr.WriteString("ID3")
	hdr.WriteByte(4) // major version 2.4
	hdr.WriteByte(0) // revision
	hdr.WriteByte(0) // flags
	// Maximum 28-bit syncsafe value => ~256 MiB declared tag size.
	hdr.Write([]byte{0x7f, 0x7f, 0x7f, 0x7f})
	// Only a few real bytes follow; nothing close to the declared size.
	hdr.WriteString("short")

	// Small budget. Before the clamp, Extract allocated 10+256MiB here.
	chunks, err := NewID3Extractor().Extract(bytes.NewReader(hdr.Bytes()), 64*1024)
	if err == nil {
		t.Fatalf("expected a short-read error for a truncated oversized tag, got chunks=%v", chunks)
	}
	if chunks != nil {
		t.Fatalf("expected nil chunks on error, got %v", chunks)
	}
}
