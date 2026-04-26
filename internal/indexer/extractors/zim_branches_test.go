package extractors

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// patchDirEntryMimeIdx rewrites the 2-byte mimeIdx field of the
// i-th dir entry inside a ZIM file produced by buildTestZim. The
// caller passes the bytes back so we don't need to know the
// internal layout twice.
func patchDirEntryMimeIdx(t *testing.T, zim []byte, i int, newMimeIdx uint16) []byte {
	t.Helper()
	// Read the URL pointer list to find the dir entry's offset.
	hdr := readZimHeaderForTest(t, zim)
	urlPtrAt := int64(hdr.URLPtrPos) + int64(i)*8
	dePos := int64(binary.LittleEndian.Uint64(zim[urlPtrAt : urlPtrAt+8]))
	binary.LittleEndian.PutUint16(zim[dePos:dePos+2], newMimeIdx)
	return zim
}

// readZimHeaderForTest is a tiny helper that decodes just the
// fields patchDirEntryMimeIdx needs.
func readZimHeaderForTest(t *testing.T, zim []byte) *zimHeader {
	t.Helper()
	r := bytes.NewReader(zim)
	hdr, err := readZimHeader(r)
	if err != nil {
		t.Fatalf("readZimHeader: %v", err)
	}
	return hdr
}

// TestZimExtractorSkipsRedirectAndDeletedEntries covers two
// continue arms in Extract:
//
//	if entry.IsRedirect || entry.IsDeleted { continue }
//	if int(entry.MimeIdx) >= len(mimes) { continue }
//
// Build a 3-article ZIM, then rewrite article 1's mimeIdx to
// 0xFFFF (redirect) and article 2's to a value larger than
// len(mimes) so the bounds-check skip fires too. The remaining
// good article (index 0) must still extract.
func TestZimExtractorSkipsRedirectAndDeletedEntries(t *testing.T) {
	t.Parallel()
	articles := []zimTestArticle{
		{URL: "good.txt", Mime: "text/plain", Body: []byte("hello world")},
		{URL: "redir.txt", Mime: "text/plain", Body: []byte("does not matter")},
		{URL: "outofrange.txt", Mime: "text/plain", Body: []byte("does not matter")},
	}
	zim := buildTestZim(t, articles, "text/plain")
	zim = patchDirEntryMimeIdx(t, zim, 1, zimRedirectMime)
	zim = patchDirEntryMimeIdx(t, zim, 2, 99) // > len(mimes)

	chunks, err := NewZimExtractor().Extract(bytes.NewReader(zim), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1 (the redirect + bad-mime entries must be skipped)", len(chunks))
	}
	if !strings.Contains(chunks[0].Text, "hello world") {
		t.Errorf("unexpected text: %q", chunks[0].Text)
	}
}

// TestZimExtractorSkipsNonExtractableMime covers the
// `if !zimIsExtractableMime(mimes[entry.MimeIdx]) { continue }`
// arm. Build a ZIM whose only MIME is image/png — no extractor
// claims it, so the loop body returns nil chunks.
func TestZimExtractorSkipsNonExtractableMime(t *testing.T) {
	t.Parallel()
	articles := []zimTestArticle{
		{URL: "logo.png", Mime: "image/png", Body: []byte{0x89, 'P', 'N', 'G'}},
	}
	zim := buildTestZim(t, articles, "image/png")

	chunks, err := NewZimExtractor().Extract(bytes.NewReader(zim), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if chunks != nil {
		t.Errorf("got %d chunks, want nil for non-extractable MIME", len(chunks))
	}
}

// TestZimExtractorSkipsEmptyDecodedText covers the
// `if text == "" { continue }` arm. An article whose body is
// pure whitespace decodes to "" and must be skipped without
// emitting an empty Chunk.
func TestZimExtractorSkipsEmptyDecodedText(t *testing.T) {
	t.Parallel()
	articles := []zimTestArticle{
		{URL: "blank.txt", Mime: "text/plain", Body: []byte("    \n\t ")},
		{URL: "good.txt", Mime: "text/plain", Body: []byte("real content")},
	}
	zim := buildTestZim(t, articles, "text/plain")

	chunks, err := NewZimExtractor().Extract(bytes.NewReader(zim), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1 (whitespace-only article must be skipped)", len(chunks))
	}
	if !strings.Contains(chunks[0].Text, "real content") {
		t.Errorf("unexpected text: %q", chunks[0].Text)
	}
}

// TestZimExtractorRespectsMaxBytesCutoff covers the early-break
// arm `if emitted >= maxBytes { break }` in Extract. Build a
// 2-article ZIM whose first article alone overflows maxBytes;
// the second article must not appear.
func TestZimExtractorRespectsMaxBytesCutoff(t *testing.T) {
	t.Parallel()
	first := strings.Repeat("a", 200)
	articles := []zimTestArticle{
		{URL: "first.txt", Mime: "text/plain", Body: []byte(first)},
		{URL: "second.txt", Mime: "text/plain", Body: []byte("second article")},
	}
	zim := buildTestZim(t, articles, "text/plain")

	chunks, err := NewZimExtractor().Extract(bytes.NewReader(zim), 50) // cap below first article len
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	combined := ""
	for _, c := range chunks {
		combined += c.Text
	}
	if strings.Contains(combined, "second article") {
		t.Errorf("maxBytes break did not fire — second article present:\n%s", combined)
	}
}
