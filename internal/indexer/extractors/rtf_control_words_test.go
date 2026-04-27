package extractors

import (
	"strings"
	"testing"
)

// TestRTFExtractorControlWordsLineBreaksAndPunctuation drives
// every reachable single-line RTF control word through the
// switch in Extract:
//
//	\par, \line, \sect, \page → newline
//	\tab → tab
//	\emdash, \endash, \lquote, \rquote, \ldblquote, \rdblquote, \bullet
//	\'HH (hex byte escape, here \\'a9 = U+00A9 ©)
//	literal \\\\ \\{ \\} escapes
//
// One synthesised RTF body, one Extract, multiple substring
// assertions. Each expected glyph appearing in the output proves
// the matching switch arm fired.
func TestRTFExtractorControlWordsLineBreaksAndPunctuation(t *testing.T) {
	t.Parallel()
	body := `{\rtf1\ansi alpha\par beta\line gamma\sect delta\page eps\tab tab` +
		` em\emdash en\endash lq\lquote rq\rquote ldq\ldblquote rdq\rdblquote` +
		` bul\bullet hex\'a9 lit\\backslash lit\{ lit\} bye}`
	chunks, err := NewRTFExtractor().Extract(strings.NewReader(body), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}
	got := chunks[0].Text

	wants := []string{
		"\n",   // \par/\line/\sect/\page
		"\t",   // \tab
		"—",    // \emdash
		"–",    // \endash
		"‘",    // \lquote
		"’",    // \rquote
		"“",    // \ldblquote
		"”",    // \rdblquote
		"•",    // \bullet
		"©",    // \'a9
		"\\",   // \\
		"{",    // \{
		"}",    // \}
		"alpha", "beta", "gamma", "delta", "eps", "bye", // literal text passes through
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in output:\n%s", w, got)
		}
	}
}

// TestRTFDispatchByMIME covers the
// `if mime == "application/rtf" || mime == "text/rtf" { return true }`
// arm in the rtf init's claims function. Existing tests reach the
// extractor only through the .rtf extension fallback.
//
// Only the application/rtf MIME goes through the rtf extractor
// directly; "text/rtf" is claimed by the plaintext extractor first
// (any "text/*" MIME wins ahead in the dispatch order). We exercise
// the rtf MIME branch via application/rtf.
func TestRTFDispatchByMIME(t *testing.T) {
	t.Parallel()
	got, _ := Dispatch(Candidate{Path: "no-ext-here", MIME: "application/rtf", Size: 1024})
	if got == nil || got.Name() != "rtf" {
		t.Errorf("Dispatch(application/rtf) = %v, want rtf extractor", got)
	}
}
