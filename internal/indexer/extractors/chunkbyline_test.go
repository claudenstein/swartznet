package extractors

import (
	"strings"
	"testing"
)

// TestChunkByLineMultiLineFlush exercises the previously-uncovered
// "current chunk would overflow if we add this line" branch
// (chunker.go:186-189). We hand the helper a paragraph composed of
// many short lines whose cumulative size exceeds targetBytes, so
// the function flushes the current builder and starts a new chunk
// at a clean line boundary instead of hard-splitting.
func TestChunkByLineMultiLineFlush(t *testing.T) {
	t.Parallel()

	target := 200
	maxChunk := 600 // > target so individual lines never trip the hard-split path
	// Each line is 50 bytes. After 4 lines we're at 200 bytes; the
	// 5th line would push past target → flush expected.
	const lineLen = 50
	line := strings.Repeat("a", lineLen-1) + "\n"
	text := strings.Repeat(line, 12) // 12 lines, 600 bytes

	chunks := chunkByLine(text, target, maxChunk)
	if len(chunks) < 2 {
		t.Fatalf("got %d chunks, want at least 2 (flush should split)", len(chunks))
	}
	// No chunk may exceed targetBytes + one extra line worth of slack.
	// We just assert nothing came back larger than maxChunk and the
	// joined output preserves every byte.
	var joined strings.Builder
	var prevOff int64 = -1
	for i, c := range chunks {
		if len(c.Text) > maxChunk {
			t.Errorf("chunk %d exceeds maxChunk: %d > %d", i, len(c.Text), maxChunk)
		}
		if c.Offset <= prevOff && i > 0 {
			t.Errorf("offsets not monotonic at chunk %d: %d <= %d", i, c.Offset, prevOff)
		}
		prevOff = c.Offset
		joined.WriteString(c.Text)
	}
	if joined.String() != text {
		t.Error("joined chunks did not reproduce input byte-for-byte")
	}
}

// TestChunkByLineFinalLineNoTrailingNewline exercises the
// previously-uncovered branch where strings.IndexByte returns -1
// (chunker.go:156-157) — the final line of the paragraph has no
// terminating \n.
func TestChunkByLineFinalLineNoTrailingNewline(t *testing.T) {
	t.Parallel()

	target := 200
	maxChunk := 600
	// 5 lines, but the very last has no trailing \n.
	text := strings.Repeat("aaaa\n", 5) + "trailing-no-newline"

	chunks := chunkByLine(text, target, maxChunk)
	if len(chunks) == 0 {
		t.Fatal("got 0 chunks, want >= 1")
	}
	// Last chunk must contain the no-newline trailer.
	last := chunks[len(chunks)-1].Text
	if !strings.Contains(last, "trailing-no-newline") {
		t.Errorf("last chunk = %q, missing 'trailing-no-newline' tail", last)
	}
	// Verify byte-level preservation.
	var joined strings.Builder
	for _, c := range chunks {
		joined.WriteString(c.Text)
	}
	if joined.String() != text {
		t.Error("joined chunks did not reproduce input byte-for-byte")
	}
}
