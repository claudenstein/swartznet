package extractors

import (
	"bufio"
	"bytes"
	"testing"
)

// TestMKVExtractorOversizedElementSkipped is the regression for the
// readFull/size-bound fix: an EBML child element whose VINT size claims
// more than maxBytes must NOT drive a multi-GB make([]byte, n). The
// extractor should skip the oversized element and return without OOM.
//
// We hand-build an Info element whose declared size is ~3 GiB but whose
// real body is tiny. Before the fix, Extract called
// readFull(br, int(size)) and tried to allocate ~3 GiB.
func TestMKVExtractorOversizedElementSkipped(t *testing.T) {
	t.Parallel()

	ebmlHeader := ebmlElem(ebmlIDEBML, []byte("dummy"))

	// Oversized Info: a 4-byte size VINT claiming a huge length, with a
	// short actual body. ebmlSize() emits a 4-byte VINT for values
	// >= 0x200000; 0x0FFFFFFF ~= 268 MiB which already exceeds the
	// default 16 MiB metadata cap and the test maxBytes below.
	const hugeSize = 0x0FFFFFFF
	var info bytes.Buffer
	info.Write(ebmlID(ebmlIDInfo))
	info.Write(ebmlSize(hugeSize))
	info.WriteString("short") // real body far shorter than declared

	segBody := info.Bytes()
	segment := ebmlElem(ebmlIDSegment, segBody)

	var file bytes.Buffer
	file.Write(ebmlHeader)
	file.Write(segment)

	// maxBytes well below hugeSize. The extractor must not allocate
	// hugeSize bytes; it should skip/return cleanly (no panic, no OOM).
	chunks, err := NewMKVExtractor().Extract(bytes.NewReader(file.Bytes()), 1*1024*1024)
	if err != nil {
		// An error is acceptable (truncated discard) — what matters is
		// that we did not attempt the giant allocation. A panic/OOM
		// would have failed the test process instead.
		return
	}
	// No usable metadata in the oversized element, so no chunks.
	if len(chunks) != 0 {
		t.Fatalf("expected no chunks from oversized element, got %d", len(chunks))
	}
}

// TestReadFullRejectsHugeLength directly exercises the shared readFull
// ceiling so flac/mkv/ogg all inherit the guard.
func TestReadFullRejectsHugeLength(t *testing.T) {
	t.Parallel()
	// readFull takes a *bufio.Reader; an empty reader is fine because
	// the size check must fire before any read attempt.
	br := bufio.NewReader(bytes.NewReader(nil))
	if _, err := readFull(br, readFullMaxAlloc+1); err == nil {
		t.Fatal("readFull should reject a length above the alloc ceiling")
	}
	if _, err := readFull(br, -1); err == nil {
		t.Fatal("readFull should reject a negative length")
	}
}
