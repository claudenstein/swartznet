package extractors

import (
	"bytes"
	"testing"
)

// TestEPUBExtractorEmptyChaptersReturnNoChunks covers the
// `if strings.TrimSpace(combined.String()) == "" { return nil, nil }`
// arm in EPUBExtractor.Extract. An EPUB with chapter files
// whose body is empty/whitespace-only must surface as (nil, nil).
func TestEPUBExtractorEmptyChaptersReturnNoChunks(t *testing.T) {
	t.Parallel()
	chapters := map[string]string{
		"chapter01.xhtml": `<html><body>   </body></html>`,
		"chapter02.xhtml": `<html><body></body></html>`,
	}
	data := buildMinimalEPUB(t, chapters)

	chunks, err := NewEPUBExtractor().Extract(bytes.NewReader(data), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if chunks != nil {
		t.Errorf("got %d chunks, want nil for whitespace-only chapters", len(chunks))
	}
}
