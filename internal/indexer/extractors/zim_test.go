package extractors

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// TestZimExtractorBasic builds a minimal valid ZIM file with two
// HTML articles in one uncompressed cluster, then verifies the
// extractor pulls the visible text out of both.
func TestZimExtractorBasic(t *testing.T) {
	t.Parallel()

	articles := []zimTestArticle{
		{URL: "moby_dick.html", Mime: "text/html", Body: []byte("<html><body><h1>Moby-Dick</h1><p>Call me Ishmael.</p></body></html>")},
		{URL: "pride.html", Mime: "text/html", Body: []byte("<html><body><h1>Pride and Prejudice</h1><p>It is a truth universally acknowledged.</p></body></html>")},
	}
	zim := buildTestZim(t, articles, "text/html")

	chunks, err := NewZimExtractor().Extract(bytes.NewReader(zim), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) != len(articles) {
		t.Fatalf("got %d chunks, want %d", len(chunks), len(articles))
	}
	all := ""
	for _, c := range chunks {
		all += c.Text + "\n"
	}
	for _, want := range []string{"Moby-Dick", "Call me Ishmael", "Pride and Prejudice", "universally acknowledged"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in extracted text:\n%s", want, all)
		}
	}
	// HTML tags must not leak through.
	if strings.Contains(all, "<h1>") || strings.Contains(all, "<body>") {
		t.Errorf("raw HTML leaked into output:\n%s", all)
	}
}

// TestZimExtractorDispatchByExtension checks the registry routes
// .zim files to this extractor.
func TestZimExtractorDispatchByExtension(t *testing.T) {
	t.Parallel()
	e, _ := Dispatch(Candidate{Path: "wikipedia.zim", Size: 100 * 1024 * 1024})
	if e == nil || e.Name() != "zim" {
		t.Errorf("got extractor=%v, want zim", e)
	}
}

// TestZimExtractorRejectsNonReaderAt confirms the extractor refuses
// streaming readers — random access is required for a 70 GiB file.
func TestZimExtractorRejectsNonReaderAt(t *testing.T) {
	t.Parallel()
	// streamReader wraps *bytes.Reader but hides its ReaderAt
	// method; the extractor should reject non-seekable input.
	r := streamReader{r: bytes.NewReader([]byte("ZIM\x04stub"))}
	_, err := NewZimExtractor().Extract(r, 0)
	if err == nil {
		t.Error("expected error for non-ReaderAt input")
	}
}

// TestZimExtractorBadMagic confirms a clean error on non-ZIM input.
func TestZimExtractorBadMagic(t *testing.T) {
	t.Parallel()
	junk := bytes.Repeat([]byte{0xff}, 200)
	_, err := NewZimExtractor().Extract(bytes.NewReader(junk), 0)
	if err == nil {
		t.Error("expected magic mismatch error")
	}
}

// TestZimExtractorPlaintextMime exercises the text/plain branch
// (skips the HTML decoder).
func TestZimExtractorPlaintextMime(t *testing.T) {
	t.Parallel()
	articles := []zimTestArticle{
		{URL: "readme.txt", Mime: "text/plain", Body: []byte("This is plain text content.\nLine two.")},
	}
	zim := buildTestZim(t, articles, "text/plain")

	chunks, err := NewZimExtractor().Extract(bytes.NewReader(zim), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	if !strings.Contains(chunks[0].Text, "plain text content") {
		t.Errorf("missing expected text in:\n%s", chunks[0].Text)
	}
}

// streamReader hides ReaderAt from a *bytes.Reader so the extractor
// sees only an io.Reader.
type streamReader struct{ r *bytes.Reader }

func (s streamReader) Read(p []byte) (int, error) { return s.r.Read(p) }

// zimTestArticle is one synthetic article inserted into the test ZIM.
type zimTestArticle struct {
	URL  string
	Mime string
	Body []byte
}

// buildTestZim constructs a minimal valid ZIM file in memory with
// the given articles in a single uncompressed cluster. mime0 picks
// which MIME type lands at index 0 in the MIME list (every article
// must use this MIME — the synthesizer is intentionally simple).
//
// Layout:
//
//	[header 80 B][mime list][URL ptr list][cluster ptr list][dir entries][cluster][checksum]
func buildTestZim(t *testing.T, articles []zimTestArticle, mime0 string) []byte {
	t.Helper()
	if len(articles) == 0 {
		t.Fatal("buildTestZim needs at least one article")
	}
	for _, a := range articles {
		if a.Mime != mime0 {
			t.Fatalf("test synthesizer only supports a single MIME (got %q, want %q)", a.Mime, mime0)
		}
	}

	const headerSize = 80
	mimeList := append([]byte(mime0), 0, 0) // single mime + terminator (empty string + null)

	// Build the cluster body: offset table (uint32 LE, blob_count+1 entries)
	// followed by each blob.
	blobCount := len(articles)
	offsetTableBytes := (blobCount + 1) * 4
	clusterBodyParts := make([][]byte, 0, 2+blobCount)
	offsets := make([]uint32, 0, blobCount+1)
	cursor := uint32(offsetTableBytes)
	for _, a := range articles {
		offsets = append(offsets, cursor)
		cursor += uint32(len(a.Body))
	}
	offsets = append(offsets, cursor) // sentinel == end of last blob
	otBuf := new(bytes.Buffer)
	for _, off := range offsets {
		_ = binary.Write(otBuf, binary.LittleEndian, off)
	}
	clusterBodyParts = append(clusterBodyParts, otBuf.Bytes())
	for _, a := range articles {
		clusterBodyParts = append(clusterBodyParts, a.Body)
	}
	clusterBody := bytes.Join(clusterBodyParts, nil)
	cluster := append([]byte{1}, clusterBody...) // type=1 uncompressed, no extended bit

	// Compute layout positions.
	mimePos := uint64(headerSize)
	urlPtrPos := mimePos + uint64(len(mimeList))
	clusterPtrPos := urlPtrPos + uint64(len(articles)*8)

	// Each dir entry: 16-byte fixed prefix + url + \0 + title + \0 (we use
	// title="" so only a trailing null follows the url+null).
	dirEntries := make([][]byte, 0, len(articles))
	for i, a := range articles {
		var entry bytes.Buffer
		_ = binary.Write(&entry, binary.LittleEndian, uint16(0)) // mimeIdx=0
		entry.WriteByte(0)                                       // parameter len
		entry.WriteByte('A')                                     // namespace 'A'=articles
		_ = binary.Write(&entry, binary.LittleEndian, uint32(0)) // revision
		_ = binary.Write(&entry, binary.LittleEndian, uint32(0)) // cluster=0
		_ = binary.Write(&entry, binary.LittleEndian, uint32(i)) // blob=i
		entry.WriteString(a.URL)
		entry.WriteByte(0)
		entry.WriteByte(0) // empty title + terminator
		dirEntries = append(dirEntries, entry.Bytes())
	}

	dirEntriesPos := clusterPtrPos + 8 // single cluster ptr
	dirEntryOffsets := make([]uint64, 0, len(articles))
	cur := dirEntriesPos
	for _, de := range dirEntries {
		dirEntryOffsets = append(dirEntryOffsets, cur)
		cur += uint64(len(de))
	}
	clusterPos := cur

	checksumPos := clusterPos + uint64(len(cluster))

	// Assemble.
	out := new(bytes.Buffer)
	// Header
	_ = binary.Write(out, binary.LittleEndian, uint32(zimMagic))
	_ = binary.Write(out, binary.LittleEndian, uint16(5))             // major version
	_ = binary.Write(out, binary.LittleEndian, uint16(0))             // minor version
	out.Write(make([]byte, 16))                                       // UUID (zeros)
	_ = binary.Write(out, binary.LittleEndian, uint32(len(articles))) // articleCount
	_ = binary.Write(out, binary.LittleEndian, uint32(1))             // clusterCount
	_ = binary.Write(out, binary.LittleEndian, urlPtrPos)
	_ = binary.Write(out, binary.LittleEndian, urlPtrPos) // titlePtrPos — reuse, unused by extractor
	_ = binary.Write(out, binary.LittleEndian, clusterPtrPos)
	_ = binary.Write(out, binary.LittleEndian, mimePos)
	_ = binary.Write(out, binary.LittleEndian, uint32(0)) // mainPage
	_ = binary.Write(out, binary.LittleEndian, uint32(0)) // layoutPage
	_ = binary.Write(out, binary.LittleEndian, checksumPos)

	// Body
	out.Write(mimeList)
	for _, off := range dirEntryOffsets {
		_ = binary.Write(out, binary.LittleEndian, off)
	}
	_ = binary.Write(out, binary.LittleEndian, clusterPos) // single cluster ptr
	for _, de := range dirEntries {
		out.Write(de)
	}
	out.Write(cluster)
	out.Write(make([]byte, 16)) // dummy MD5 checksum (zeros)
	return out.Bytes()
}
