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

// TestZimDispatchByExtensionFallback covers the
// `strings.EqualFold(filepath.Ext(c.Path), ".zim") { return true }`
// arm in zim's init claims. The existing dispatch test goes
// through Dispatch which fills MIME from the path, so the
// MIME-prefix arm above always fires first. Pass a
// non-zim MIME so only the extension-fallback arm matches.
func TestZimDispatchByExtensionFallback(t *testing.T) {
	t.Parallel()
	got, _ := Dispatch(Candidate{Path: "wikipedia.zim", MIME: "application/octet-stream", Size: 1024})
	if got == nil || got.Name() != "zim" {
		t.Errorf("Dispatch(.zim, octet-stream) = %v, want zim extractor", got)
	}
}

// patchClusterTypeByte rewrites the cluster's type byte (the
// first byte of the cluster blob). cluster 0's offset lives in
// the cluster ptr list at clusterPtrPos+0*8.
func patchClusterTypeByte(t *testing.T, zim []byte, clusterIdx uint32, newType byte) []byte {
	t.Helper()
	hdr := readZimHeaderForTest(t, zim)
	cpAt := int64(hdr.ClusterPtrPos) + int64(clusterIdx)*8
	clusterPos := int64(binary.LittleEndian.Uint64(zim[cpAt : cpAt+8]))
	zim[clusterPos] = newType
	return zim
}

// TestZimExtractorSkipsDeletedEntry covers readZimDirEntry's
// `case zimDeletedMime: return zimDirEntry{IsDeleted: true}` arm.
// Patch a built ZIM's dir entry mimeIdx to zimDeletedMime; the
// outer Extract loop then takes the IsDeleted skip branch.
func TestZimExtractorSkipsDeletedEntry(t *testing.T) {
	t.Parallel()
	articles := []zimTestArticle{
		{URL: "good.txt", Mime: "text/plain", Body: []byte("hello world")},
		{URL: "del.txt", Mime: "text/plain", Body: []byte("does not matter")},
	}
	zim := buildTestZim(t, articles, "text/plain")
	zim = patchDirEntryMimeIdx(t, zim, 1, zimDeletedMime)

	chunks, err := NewZimExtractor().Extract(bytes.NewReader(zim), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1 (deleted entry must be skipped)", len(chunks))
	}
	if !strings.Contains(chunks[0].Text, "hello world") {
		t.Errorf("unexpected text: %q", chunks[0].Text)
	}
}

// TestZimExtractorReadHeaderTruncated covers readZimHeader's
// `_, err := ra.ReadAt(buf[:], 0); if err != nil { ... }` arm.
// A reader shorter than the 80-byte header forces ReadAt to
// return an error before the magic check runs.
func TestZimExtractorReadHeaderTruncated(t *testing.T) {
	t.Parallel()
	short := []byte("ZIM\x04tooshort") // 12 bytes < 80
	if _, err := NewZimExtractor().Extract(bytes.NewReader(short), 0); err == nil {
		t.Error("Extract should fail when input is shorter than header")
	}
}

// TestZimExtractorDirEntryReadFailsContinue covers Extract's
// `entry, err := readZimDirEntry(...); if err != nil { continue }`
// arm. Patch entry 0's URL pointer to point past EOF; the dir
// entry read fails and Extract skips that article. The second
// article's pointer is intact so it still extracts.
func TestZimExtractorDirEntryReadFailsContinue(t *testing.T) {
	t.Parallel()
	articles := []zimTestArticle{
		{URL: "bad.txt", Mime: "text/plain", Body: []byte("never seen")},
		{URL: "good.txt", Mime: "text/plain", Body: []byte("hello-good")},
	}
	zim := buildTestZim(t, articles, "text/plain")
	hdr := readZimHeaderForTest(t, zim)
	// Overwrite entry 0's URL pointer (8 bytes at URLPtrPos+0*8)
	// to a position past the file end so the dir-entry ReadAt
	// inside readZimDirEntry returns an error.
	urlPtrAt := int64(hdr.URLPtrPos)
	binary.LittleEndian.PutUint64(zim[urlPtrAt:urlPtrAt+8], uint64(len(zim))+1<<20)

	chunks, err := NewZimExtractor().Extract(bytes.NewReader(zim), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1 (the bad-pointer entry must be skipped)", len(chunks))
	}
	if !strings.Contains(chunks[0].Text, "hello-good") {
		t.Errorf("unexpected text: %q", chunks[0].Text)
	}
}

// TestZimExtractorMimeListReadFails covers readZimMimeList's
// `if err != nil { break }` + `if len(all) == 0 { error }`
// arms when the mime-list position points past the file end.
// Extract surfaces the wrapped error.
func TestZimExtractorMimeListReadFails(t *testing.T) {
	t.Parallel()
	articles := []zimTestArticle{
		{URL: "x.txt", Mime: "text/plain", Body: []byte("content")},
	}
	zim := buildTestZim(t, articles, "text/plain")
	// Patch MimeListPos to a position past EOF. Header offset
	// 56-64 holds MimeListPos (uint64 LE).
	binary.LittleEndian.PutUint64(zim[56:64], uint64(len(zim))+1<<20)

	if _, err := NewZimExtractor().Extract(bytes.NewReader(zim), 0); err == nil {
		t.Error("Extract should fail when MimeListPos is past EOF")
	}
}

// TestZimExtractorXZClusterTypeRejected covers readZimCluster's
// `zimCompXZ → "XZ/LZMA2 clusters not supported in v1"` arm.
// Patch a valid uncompressed cluster's type byte to 4 (XZ); the
// readZimCluster err is swallowed by Extract's `continue`, so
// the resulting chunk list is empty.
func TestZimExtractorXZClusterTypeRejected(t *testing.T) {
	t.Parallel()
	articles := []zimTestArticle{
		{URL: "x.txt", Mime: "text/plain", Body: []byte("content")},
	}
	zim := buildTestZim(t, articles, "text/plain")
	zim = patchClusterTypeByte(t, zim, 0, 4) // XZ

	chunks, err := NewZimExtractor().Extract(bytes.NewReader(zim), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if chunks != nil {
		t.Errorf("got %d chunks, want nil for XZ-typed cluster", len(chunks))
	}
}

// TestZimExtractorUnknownClusterTypeRejected covers readZimCluster's
// `default → "unknown cluster compression"` arm. Patch the type
// byte to a value outside {1, 2, 4} so the switch's default fires.
func TestZimExtractorUnknownClusterTypeRejected(t *testing.T) {
	t.Parallel()
	articles := []zimTestArticle{
		{URL: "x.txt", Mime: "text/plain", Body: []byte("content")},
	}
	zim := buildTestZim(t, articles, "text/plain")
	zim = patchClusterTypeByte(t, zim, 0, 6) // unknown

	chunks, err := NewZimExtractor().Extract(bytes.NewReader(zim), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if chunks != nil {
		t.Errorf("got %d chunks, want nil for unknown cluster type", len(chunks))
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
