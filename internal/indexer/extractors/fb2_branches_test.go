package extractors

import (
	"strings"
	"testing"
)

// TestFB2ExtractorMalformedXML covers the
// `tok, err := dec.Token(); if err != nil { return nil, err }`
// arm. Token-level XML garbage forces a decode error.
func TestFB2ExtractorMalformedXML(t *testing.T) {
	t.Parallel()
	if _, err := NewFB2Extractor().Extract(strings.NewReader("<<<garbage"), 0); err == nil {
		t.Error("Extract should fail on token-level XML garbage")
	}
}

// TestFB2ExtractorEmptyTextReturnsNil covers the
// `if strings.TrimSpace(text) == "" { return nil, nil }` arm.
// FB2 with only <binary> content (always skipped) yields no
// extractable text.
func TestFB2ExtractorEmptyTextReturnsNil(t *testing.T) {
	t.Parallel()
	body := `<?xml version="1.0"?><FictionBook>` +
		`<binary id="cover.png" content-type="image/png">base64data</binary>` +
		`</FictionBook>`
	chunks, err := NewFB2Extractor().Extract(strings.NewReader(body), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if chunks != nil {
		t.Errorf("got %d chunks, want nil for binary-only FB2", len(chunks))
	}
}
