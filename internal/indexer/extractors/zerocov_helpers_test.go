package extractors

import "testing"

// TestRTFExtractorName pins the Name() string so the
// pipeline-side `extractor` field is stable across refactors.
// It also covers the previously-uncovered Name method.
func TestRTFExtractorName(t *testing.T) {
	t.Parallel()
	if got := NewRTFExtractor().Name(); got != "rtf" {
		t.Errorf("Name() = %q, want \"rtf\"", got)
	}
}

// TestGetZstdDecoderInitializes covers the lazy zstd-decoder
// constructor used by the ZIM cluster reader. The decoder is
// process-shared (sync.Once) so we only verify the call returns
// a non-nil decoder without error; subsequent ZIM tests reuse
// the same instance.
func TestGetZstdDecoderInitializes(t *testing.T) {
	t.Parallel()
	d, err := getZstdDecoder()
	if err != nil {
		t.Fatalf("getZstdDecoder: %v", err)
	}
	if d == nil {
		t.Fatal("getZstdDecoder returned nil decoder with no error")
	}
}
