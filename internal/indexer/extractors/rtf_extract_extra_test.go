package extractors

import (
	"strings"
	"testing"
)

// TestRTFExtractorMaxBytesCutoff covers the `out.Len() > maxBytes
// → break` guard. We feed a document longer than the requested
// cap and check that extraction returns without error and without
// including the late-arriving marker.
func TestRTFExtractorMaxBytesCutoff(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	b.WriteString(`{\rtf1\ansi `)
	b.WriteString(strings.Repeat("Early ", 200))
	b.WriteString(`LATE_SENTINEL_Z`)
	b.WriteString(`}`)

	chunks, err := NewRTFExtractor().Extract(strings.NewReader(b.String()), 64)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	combined := strings.Join(chunkTexts(chunks), "\n")
	if strings.Contains(combined, "LATE_SENTINEL_Z") {
		t.Errorf("extractor ignored maxBytes cutoff: %q", combined)
	}
	if !strings.Contains(combined, "Early") {
		t.Errorf("expected early text, got %q", combined)
	}
}

// TestRTFExtractorSkipsLiteralInsideSkippedGroup covers the
// `skipDepth > 0 → continue` branch at the default character
// arm. Plain letters inside \fonttbl{...} must not leak out.
func TestRTFExtractorSkipsLiteralInsideSkippedGroup(t *testing.T) {
	t.Parallel()
	rtf := `{\rtf1\ansi{\fonttbl UNSEEN_LITERAL_TEXT}real body\par}`
	chunks, err := NewRTFExtractor().Extract(strings.NewReader(rtf), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	combined := strings.Join(chunkTexts(chunks), "\n")
	if strings.Contains(combined, "UNSEEN_LITERAL_TEXT") {
		t.Errorf("skipDepth failed to hide literal text: %q", combined)
	}
	if !strings.Contains(combined, "real body") {
		t.Errorf("body missing: %q", combined)
	}
}

// TestRTFExtractorDiscardableDestination covers the `\*` case —
// a discardable destination should be skipped entirely along with
// any literal text inside its containing group.
func TestRTFExtractorDiscardableDestination(t *testing.T) {
	t.Parallel()
	rtf := `{\rtf1\ansi {\*\generator LibreOffice-hidden-literal}visible body\par}`
	chunks, err := NewRTFExtractor().Extract(strings.NewReader(rtf), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	combined := strings.Join(chunkTexts(chunks), "\n")
	if strings.Contains(combined, "LibreOffice-hidden-literal") {
		t.Errorf("discardable destination leaked: %q", combined)
	}
	if !strings.Contains(combined, "visible body") {
		t.Errorf("visible body missing: %q", combined)
	}
}
