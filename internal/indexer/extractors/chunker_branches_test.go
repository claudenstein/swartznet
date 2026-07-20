package extractors

import (
	"strings"
	"testing"
)

// TestChunkTextZeroTargetBytesUsesDefault covers the
// `if targetBytes <= 0 { targetBytes = DefaultChunkTargetBytes }`
// arm. Pass 0 to force the default; the result must match a
// direct call with the default cap.
func TestChunkTextZeroTargetBytesUsesDefault(t *testing.T) {
	t.Parallel()
	// Build a body big enough that the small-file early return is
	// not taken when DefaultChunkTargetBytes is in force.
	body := strings.Repeat("para1 line one.\n\npara2 line two.\n\n",
		200) // > DefaultChunkTargetBytes * smallFileFactor
	a := chunkText(body, 0)
	b := chunkText(body, DefaultChunkTargetBytes)
	if len(a) == 0 {
		t.Fatal("chunkText with targetBytes=0 returned no chunks")
	}
	if len(a) != len(b) {
		t.Errorf("chunkText(0) produced %d chunks; chunkText(DefaultChunkTargetBytes) produced %d",
			len(a), len(b))
	}
}

// TestChunkTextJoinsParagraphsWithBlankLine covers the
// `if curBuf.Len() > 0 { curBuf.WriteString("\\n\\n") }` arm. Two
// short paragraphs must end up joined by a blank line in a
// single chunk (when both fit under targetBytes).
//
// The body must be larger than smallFileFactor*targetBytes so the
// chunker actually runs (rather than returning a single Chunk
// directly), and individual paragraphs must each be small enough
// that two of them fit under targetBytes — that's what triggers
// the WriteString("\\n\\n") arm.
func TestChunkTextJoinsParagraphsWithBlankLine(t *testing.T) {
	t.Parallel()
	short := "ab cd ef." // 9 bytes
	var body string
	for i := 0; i < 8; i++ {
		body += short
		if i < 7 {
			body += "\n\n"
		}
	}
	// body = 9*8 + 2*7 = 86 bytes. With targetBytes=50, smallFileFactor*50=62.5,
	// 86 > 62.5 so chunker runs. Two paragraphs (9+2+9=20 bytes) easily fit
	// under 50 → join arm fires.
	chunks := chunkText(body, 50)
	combined := ""
	for _, c := range chunks {
		combined += c.Text + "|"
	}
	// At least one chunk must contain two paragraphs joined by "\n\n".
	for _, c := range chunks {
		if strings.Contains(c.Text, short+"\n\n"+short) {
			return // arm fired
		}
	}
	t.Errorf("paragraph-join arm not exercised; combined chunks: %s", combined)
}

// TestChunkByLineFlushesBeforeOversizedLine covers the
// `if cur.Len() > 0 { ... cur.Reset() }` arm in chunkByLine. The
// arm fires when, mid-paragraph, the next line exceeds maxChunk
// AND we already have buffered content. Force this by building a
// single paragraph (no blank line) containing a short line
// followed by a line longer than chunkMaxOverrunRatio*targetBytes.
func TestChunkByLineFlushesBeforeOversizedLine(t *testing.T) {
	t.Parallel()
	short := strings.Repeat("a", 50)   // small first line
	huge := strings.Repeat("b", 200)   // > maxChunk = 100*1.5 = 150
	body := short + "\n" + huge + "\n" // single paragraph (no "\n\n")
	chunks := chunkText(body, 100)
	if len(chunks) < 2 {
		t.Fatalf("expected ≥2 chunks (flush + hard splits of the long line); got %d", len(chunks))
	}
	// The first chunk should hold the short line ("a"…), and
	// subsequent chunks should hold "b"… runs (the hard-split
	// pieces of the long line).
	if !strings.HasPrefix(chunks[0].Text, "aaa") {
		t.Errorf("chunk[0] does not start with the short line: %q", chunks[0].Text)
	}
}
