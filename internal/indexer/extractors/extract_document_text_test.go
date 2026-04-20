package extractors

import (
	"strings"
	"testing"
)

// TestExtractDocumentTextMalformedXMLErrors covers the
// xml.Decoder error-return branch in extractDocumentText.
// dec.Strict is false, so most "lenient" issues are tolerated;
// truly broken structure (mismatched tag stack) still surfaces.
func TestExtractDocumentTextMalformedXMLErrors(t *testing.T) {
	t.Parallel()
	// Truncated mid-tag: dec.Token() returns an error.
	_, err := extractDocumentText(strings.NewReader("<p><t>hi"))
	if err == nil {
		t.Error("extractDocumentText on truncated XML should error")
	}
}

// TestExtractDocumentTextHandlesCrTabBr covers the <w:cr>,
// <w:tab>, and <w:br> whitespace-substitution branches.
func TestExtractDocumentTextHandlesCrTabBr(t *testing.T) {
	t.Parallel()
	xml := `<root><p><t>hello</t><tab/><t>world</t><br/><t>!</t><cr/><t>end</t></p></root>`
	got, err := extractDocumentText(strings.NewReader(xml))
	if err != nil {
		t.Fatal(err)
	}
	// All three break/tab/cr substitute spaces between text runs.
	for _, want := range []string{"hello", "world", "!", "end"} {
		if !strings.Contains(got, want) {
			t.Errorf("got %q, missing %q", got, want)
		}
	}
}
