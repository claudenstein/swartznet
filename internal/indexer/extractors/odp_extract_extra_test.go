package extractors

import (
	"archive/zip"
	"bytes"
	"testing"
)

// TestODPExtractorEmptyInput covers the `len(buf) == 0 → nil, nil`
// short-circuit.
func TestODPExtractorEmptyInput(t *testing.T) {
	t.Parallel()
	chunks, err := NewODPExtractor().Extract(bytes.NewReader(nil), 0)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if chunks != nil {
		t.Errorf("expected nil chunks, got %v", chunks)
	}
}

// TestODPExtractorInvalidZip covers the `zip.NewReader` error
// branch — garbage bytes of plausible length.
func TestODPExtractorInvalidZip(t *testing.T) {
	t.Parallel()
	junk := bytes.Repeat([]byte{0xaa}, 64)
	_, err := NewODPExtractor().Extract(bytes.NewReader(junk), 0)
	if err == nil {
		t.Error("expected zip open error")
	}
}

// TestODPExtractorMissingContentXML covers the `content == nil →
// nil, nil` branch when the zip has no content.xml entry.
func TestODPExtractorMissingContentXML(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, _ := zw.Create("META-INF/manifest.xml")
	_, _ = f.Write([]byte("<manifest/>"))
	_ = zw.Close()

	chunks, err := NewODPExtractor().Extract(bytes.NewReader(buf.Bytes()), 0)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if chunks != nil {
		t.Errorf("expected nil chunks, got %v", chunks)
	}
}

// TestODPExtractorEmptyContentBody covers the `TrimSpace(text) ==
// "" → nil, nil` branch — content.xml present but contains no
// extractable paragraph text.
func TestODPExtractorEmptyContentBody(t *testing.T) {
	t.Parallel()
	empty := `<?xml version="1.0"?><office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0"><office:body><office:presentation/></office:body></office:document-content>`

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, _ := zw.Create("content.xml")
	_, _ = f.Write([]byte(empty))
	_ = zw.Close()

	chunks, err := NewODPExtractor().Extract(bytes.NewReader(buf.Bytes()), 0)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if chunks != nil {
		t.Errorf("expected nil chunks, got %v", chunks)
	}
}

// TestODPExtractorMaxBytesClamp covers the `maxBytes > odpMax →
// clamp` branch. We can't observe the clamp directly but we can
// ensure a huge maxBytes doesn't break extraction.
func TestODPExtractorMaxBytesClamp(t *testing.T) {
	t.Parallel()
	content := `<?xml version="1.0"?><office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0"><office:body><office:presentation><text:p>Hello</text:p></office:presentation></office:body></office:document-content>`
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, _ := zw.Create("content.xml")
	_, _ = f.Write([]byte(content))
	_ = zw.Close()

	chunks, err := NewODPExtractor().Extract(bytes.NewReader(buf.Bytes()), 1<<40)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(chunks) == 0 {
		t.Error("expected chunks from valid content")
	}
}
