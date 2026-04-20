package extractors

import "testing"

// TestNextParagraphCRLFBlankLine covers the previously-uncovered
// `\r\n\r\n` paragraph-separator branch in nextParagraph. CRLF
// is the wire-form on Windows-edited text files; the chunker
// must split on it just like \n\n.
func TestNextParagraphCRLFBlankLine(t *testing.T) {
	t.Parallel()
	// "para1" + CRLF + CRLF + "para2"
	text := "para1\r\n\r\npara2"
	first, next := nextParagraph(text, 0)
	if first != "para1" {
		t.Errorf("first paragraph = %q, want \"para1\"", first)
	}
	if next != len("para1\r\n\r\n") {
		t.Errorf("next index = %d, want %d", next, len("para1\r\n\r\n"))
	}

	// Continue from there.
	second, end := nextParagraph(text, next)
	if second != "para2" {
		t.Errorf("second paragraph = %q, want \"para2\"", second)
	}
	if end != len(text) {
		t.Errorf("end index = %d, want %d", end, len(text))
	}
}

// TestNextParagraphSkipsLeadingBlankLines covers the leading-
// blank-line skip loop. Input that begins with one or more
// "\n" / "\r" characters yields the first non-blank paragraph,
// not an empty string.
func TestNextParagraphSkipsLeadingBlankLines(t *testing.T) {
	t.Parallel()
	text := "\n\n\rstart-of-text\n\nfollow"
	first, next := nextParagraph(text, 0)
	if first != "start-of-text" {
		t.Errorf("first paragraph = %q, want \"start-of-text\"", first)
	}
	if first == "" || next == 0 {
		t.Errorf("first paragraph or next index zero (%q, %d)", first, next)
	}
}

// TestNextParagraphPosBeyondEndReturnsEmpty pins the
// pos >= len(text) early-out.
func TestNextParagraphPosBeyondEndReturnsEmpty(t *testing.T) {
	t.Parallel()
	got, end := nextParagraph("abc", 99)
	if got != "" {
		t.Errorf("got = %q, want empty", got)
	}
	if end != 99 {
		t.Errorf("end = %d, want 99 (passthrough)", end)
	}
}
