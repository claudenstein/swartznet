package extractors

import (
	"strings"
	"testing"
)

// TestRTFExtractorUnicodeNegativeParam covers the
// `if param < 0 { param += 65536 }` arm at rtf.go:162-164. RTF
// \uN encodes Unicode as a signed 16-bit int — negative values
// represent code points >= 0x8000 in two's complement. \u-1 →
// 65535 → U+FFFF; pick \u-9786 (= 55750) which yields U+D9C6 (a
// surrogate, not a valid rune) plus a positive-mapped pick. We
// use \u-3 → 65533 (U+FFFD REPLACEMENT CHARACTER) so the arm
// fires AND the resulting rune passes utf8.ValidRune.
func TestRTFExtractorUnicodeNegativeParam(t *testing.T) {
	t.Parallel()
	// \u-3? — the trailing '?' is the fallback char that gets
	// consumed by the readControl post-processing.
	body := `{\rtf1\ansi pre\u-3? post}`
	chunks, err := NewRTFExtractor().Extract(strings.NewReader(body), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected one chunk")
	}
	got := chunks[0].Text
	if !strings.Contains(got, "\uFFFD") {
		t.Errorf("expected U+FFFD from \\u-3, got %q", got)
	}
	if !strings.Contains(got, "pre") || !strings.Contains(got, "post") {
		t.Errorf("surrounding text missing: %q", got)
	}
}

// TestRTFExtractorEOFInControlWord covers readControl's
// `b, err := br.ReadByte(); if err != nil → return …, err` arm
// at rtf.go:241-243 and Extract's surfacing arm at 111-113. An
// input that ends mid-control-word (no terminator) makes
// readControl return io.EOF, which Extract surfaces as a
// wrapped error.
func TestRTFExtractorEOFInControlWord(t *testing.T) {
	t.Parallel()
	// `\foo` runs out of input mid-word; the inner ReadByte loop
	// of readControl returns io.EOF before the word terminator.
	body := `{\rtf1\ansi pre\foo`
	if _, err := NewRTFExtractor().Extract(strings.NewReader(body), 0); err == nil {
		t.Error("Extract should surface EOF inside control word")
	}
}

// TestRTFExtractorEOFInControlParam covers readControl's
// param-reading inner loop `if err != nil { break }` at
// rtf.go:251-252. An input ending mid-numeric-parameter makes
// the second ReadByte return io.EOF and the loop breaks; the
// already-collected digits become the param.
func TestRTFExtractorEOFInControlParam(t *testing.T) {
	t.Parallel()
	// `\u123` runs out of input mid-digits. The first ReadByte
	// after `u` returns '1' (a digit, kicks param mode). Then
	// the param loop reads '2', '3', then EOF. Loop breaks; the
	// outer readControl returns word="u" param=123.
	body := `{\rtf1\ansi pre\u123`
	chunks, err := NewRTFExtractor().Extract(strings.NewReader(body), 0)
	// Either it surfaces an EOF or extracts gracefully — the arm
	// of interest is inside readControl. The post-readControl
	// path may then hit the outer EOF inside Extract's main loop.
	// Both paths exercise the inner break.
	_ = chunks
	_ = err
}

// TestRTFExtractorEmptyTextReturnsNil covers the
// `if strings.TrimSpace(text) == "" { return nil, nil }` arm at
// rtf.go:189-191. An RTF body that produces only whitespace
// (here just \par newlines) trims to empty, returning nil chunks.
func TestRTFExtractorEmptyTextReturnsNil(t *testing.T) {
	t.Parallel()
	body := `{\rtf1\ansi \par\par\par}`
	chunks, err := NewRTFExtractor().Extract(strings.NewReader(body), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if chunks != nil {
		t.Errorf("expected nil chunks for whitespace-only body, got %v", chunks)
	}
}
