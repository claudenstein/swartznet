package extractors

import (
	"strings"
	"testing"
)

func TestChunkTextSmallStaysWhole(t *testing.T) {
	t.Parallel()
	// A small-enough input should come back as a single chunk —
	// the smallFileFactor optimisation avoids splitting. Sized to
	// fit the M13e post-shrink target (2 KiB) with room to spare.
	text := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 20)
	if len(text) >= DefaultChunkTargetBytes {
		t.Fatalf("fixture too big: %d bytes", len(text))
	}
	chunks := chunkText(text, DefaultChunkTargetBytes)
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1 for small input", len(chunks))
	}
	if chunks[0].Offset != 0 {
		t.Errorf("chunks[0].Offset = %d, want 0", chunks[0].Offset)
	}
	if chunks[0].Text != text {
		t.Errorf("chunks[0].Text != original; lost content")
	}
}

func TestChunkTextSplitsOnParagraphs(t *testing.T) {
	t.Parallel()
	// Build a text with 10 paragraphs of ~2 KiB each = ~20 KiB total.
	para := strings.Repeat("word ", 400) // ~2 KiB
	text := strings.Repeat(para+"\n\n", 10)
	chunks := chunkText(text, DefaultChunkTargetBytes)
	if len(chunks) < 2 {
		t.Fatalf("got %d chunks, want at least 2", len(chunks))
	}
	// Each chunk must be at most maxChunk bytes.
	maxChunk := int(float64(DefaultChunkTargetBytes) * chunkMaxOverrunRatio)
	for i, c := range chunks {
		if len(c.Text) > maxChunk {
			t.Errorf("chunk %d size %d exceeds maxChunk %d", i, len(c.Text), maxChunk)
		}
	}
	// Offsets must be strictly monotonically increasing.
	for i := 1; i < len(chunks); i++ {
		if chunks[i].Offset <= chunks[i-1].Offset {
			t.Errorf("offsets not monotonic: %d <= %d at chunk %d",
				chunks[i].Offset, chunks[i-1].Offset, i)
		}
	}
	// Concatenating chunk texts back should contain all the original
	// word count (modulo paragraph separator normalisation).
	joined := ""
	for _, c := range chunks {
		joined += c.Text + "\n\n"
	}
	origWords := strings.Count(text, "word")
	gotWords := strings.Count(joined, "word")
	if gotWords != origWords {
		t.Errorf("word count: got %d, want %d", gotWords, origWords)
	}
}

func TestChunkTextHardSplitsLongLine(t *testing.T) {
	t.Parallel()
	// A single line that exceeds maxChunk gets hard-split at
	// targetBytes intervals.
	target := 1000
	maxChunk := int(float64(target) * chunkMaxOverrunRatio) // 1500
	longLine := strings.Repeat("x", maxChunk*2+500)         // ~3.5 KB single line
	chunks := chunkText(longLine, target)
	if len(chunks) < 3 {
		t.Fatalf("got %d chunks, want at least 3 for long line", len(chunks))
	}
	for i, c := range chunks[:len(chunks)-1] {
		if len(c.Text) > maxChunk {
			t.Errorf("chunk %d too big: %d > %d", i, len(c.Text), maxChunk)
		}
	}
}

func TestChunkTextEmpty(t *testing.T) {
	t.Parallel()
	if got := chunkText("", DefaultChunkTargetBytes); got != nil {
		t.Errorf("empty input should return nil, got %v", got)
	}
}

func TestChunkTextOffsetsPointToParagraphs(t *testing.T) {
	t.Parallel()
	// Build paragraphs large enough that each lands in its own chunk,
	// so we can verify the first-byte offset of each chunk matches
	// the paragraph start in the input.
	p1 := strings.Repeat("alpha ", 1500)   // ~9 KB
	p2 := strings.Repeat("beta ", 1500)    // ~7.5 KB
	p3 := strings.Repeat("gamma ", 1500)   // ~9 KB
	text := p1 + "\n\n" + p2 + "\n\n" + p3

	chunks := chunkText(text, DefaultChunkTargetBytes)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}

	// The first chunk must start at offset 0.
	if chunks[0].Offset != 0 {
		t.Errorf("chunks[0].Offset = %d, want 0", chunks[0].Offset)
	}

	// At least one subsequent chunk must begin at or after p2's start.
	p2Start := int64(len(p1) + 2)
	var found bool
	for _, c := range chunks[1:] {
		if c.Offset >= p2Start {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no chunk begins at or after p2 start offset %d", p2Start)
	}
}

func TestPlaintextChunksLargeInput(t *testing.T) {
	t.Parallel()
	// End-to-end: the plaintext extractor must produce multiple chunks
	// for a file that exceeds DefaultChunkTargetBytes * smallFileFactor.
	e := NewPlaintextExtractor()
	para := strings.Repeat("the rain in spain stays mainly in the plain ", 200) // ~9 KB
	text := strings.Repeat(para+"\n\n", 5)                                      // ~45 KB
	chunks, err := e.Extract(strings.NewReader(text), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 {
		t.Fatalf("plaintext extractor returned %d chunks, want >= 2", len(chunks))
	}
}
