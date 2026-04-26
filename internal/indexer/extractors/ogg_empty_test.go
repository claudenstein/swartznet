package extractors

import (
	"strings"
	"testing"
)

// TestOGGExtractorEmptyInput covers the
// `page, err := readOGGPage(br); if err == io.EOF { break }`
// arm followed by `return nil, nil`. An empty stream produces
// EOF on the first read and the loop exits without finding any
// vorbis/opus packet.
func TestOGGExtractorEmptyInput(t *testing.T) {
	t.Parallel()
	chunks, err := NewOGGExtractor().Extract(strings.NewReader(""), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if chunks != nil {
		t.Errorf("got chunks for empty input, want nil")
	}
}
