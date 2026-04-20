package extractors

import (
	"strings"
	"testing"
)

// TestExtractHTMLTextSkipsScriptStyleSvg pins the documented
// noise-suppression behaviour of extractHTMLText: <script>,
// <style>, <noscript>, <svg>, <math> contents are stripped.
func TestExtractHTMLTextSkipsScriptStyleSvg(t *testing.T) {
	t.Parallel()
	html := `<html>
<head><script>var x=1;</script><style>p{color:red}</style></head>
<body>
<p>visible paragraph text</p>
<noscript>noscript-content</noscript>
<svg><text>svg-text</text></svg>
<math><mi>m</mi></math>
</body>
</html>`
	got, err := extractHTMLText(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "visible paragraph text") {
		t.Errorf("expected paragraph text in output: %q", got)
	}
	for _, noise := range []string{"var x=1", "color:red", "noscript-content", "svg-text"} {
		if strings.Contains(got, noise) {
			t.Errorf("output should not contain %q (noise tag): %q", noise, got)
		}
	}
}

// TestExtractHTMLTextSelfClosingBR covers the <br/>
// self-closing-tag branch which forces a paragraph break.
func TestExtractHTMLTextSelfClosingBR(t *testing.T) {
	t.Parallel()
	html := `<p>line one<br/>line two<br/>line three</p>`
	got, err := extractHTMLText(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "line one") || !strings.Contains(got, "line three") {
		t.Errorf("missing text in output: %q", got)
	}
	// The <br/> should produce newline boundaries; we don't
	// pin exact whitespace here, just the contract that all
	// three lines survived.
}

// TestExtractHTMLTextWhitespaceCollapse covers the whitespace
// collapsing branches in appendText: \t \n \r are normalised
// to a single space, and consecutive spaces collapse.
func TestExtractHTMLTextWhitespaceCollapse(t *testing.T) {
	t.Parallel()
	html := "<p>hello   \t\n\r  world</p>"
	got, err := extractHTMLText(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello world" {
		t.Errorf("got %q, want \"hello world\"", got)
	}
}
