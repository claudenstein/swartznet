package extractors

import (
	"strings"
	"testing"
)

// TestExtractHTMLTextTruncatesOversizedTokenGracefully pins the
// tokenizer hardening: extractHTMLText sets SetMaxBuf (the x/net/html
// default is unlimited) and limits the raw reader, and treats hitting
// either bound as graceful truncation — the text accumulated before
// the oversized token is returned, not an error, so one bomb chapter
// doesn't poison the rest of an EPUB.
func TestExtractHTMLTextTruncatesOversizedTokenGracefully(t *testing.T) {
	t.Parallel()
	// "hello" parses fine, then a single text run far larger than
	// the budget follows. The old code buffered the whole run.
	in := "<p>hello</p><p>" + strings.Repeat("a", 8192)
	got, err := extractHTMLText(strings.NewReader(in), 1024)
	if err != nil {
		t.Fatalf("oversized token should truncate gracefully, got err: %v", err)
	}
	if !strings.Contains(got, "hello") {
		t.Errorf("text before the oversized token should survive, got %q", got)
	}
	if len(got) > 1024 {
		t.Errorf("output %d bytes exceeds the 1024-byte budget", len(got))
	}
}

// TestExtractHTMLTextClampsBudget covers the maxOut clamp: zero and
// over-large budgets fall back to maxDocTextBytes and normal small
// documents extract unchanged.
func TestExtractHTMLTextClampsBudget(t *testing.T) {
	t.Parallel()
	for _, maxOut := range []int64{0, -1, int64(maxDocTextBytes) * 2} {
		got, err := extractHTMLText(strings.NewReader("<p>tiny doc</p>"), maxOut)
		if err != nil {
			t.Fatalf("maxOut=%d: %v", maxOut, err)
		}
		if got != "tiny doc" {
			t.Errorf("maxOut=%d: got %q, want \"tiny doc\"", maxOut, got)
		}
	}
}
