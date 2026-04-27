package extractors

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// patchDirEntryClusterNum rewrites the 4-byte cluster number
// field of the i-th dir entry. Layout (from buildTestZim):
// mimeIdx[2] + paramLen[1] + namespace[1] + revision[4] +
// cluster[4] + blob[4] + ... — cluster lives at offset 8.
func patchDirEntryClusterNum(t *testing.T, zim []byte, i int, newCluster uint32) []byte {
	t.Helper()
	hdr := readZimHeaderForTest(t, zim)
	urlPtrAt := int64(hdr.URLPtrPos) + int64(i)*8
	dePos := int64(binary.LittleEndian.Uint64(zim[urlPtrAt : urlPtrAt+8]))
	binary.LittleEndian.PutUint32(zim[dePos+8:dePos+12], newCluster)
	return zim
}

// patchHeaderChecksumPos rewrites the ChecksumPos field of the
// ZIM header. It lives at byte offset 72 (after the fixed 80-byte
// header layout in buildTestZim).
func patchHeaderChecksumPos(zim []byte, newPos uint64) {
	binary.LittleEndian.PutUint64(zim[72:80], newPos)
}

// patchHeaderClusterPtrPos rewrites the ClusterPtrPos field of
// the ZIM header. It lives at byte offset 48 (per buildTestZim).
func patchHeaderClusterPtrPos(zim []byte, newPos uint64) {
	binary.LittleEndian.PutUint64(zim[48:56], newPos)
}

// patchClusterPtrAt rewrites the i-th cluster pointer. The
// pointer table lives at hdr.ClusterPtrPos and each entry is 8
// bytes (uint64 LE).
func patchClusterPtrAt(t *testing.T, zim []byte, i uint32, newPos uint64) []byte {
	t.Helper()
	hdr := readZimHeaderForTest(t, zim)
	at := int64(hdr.ClusterPtrPos) + int64(i)*8
	binary.LittleEndian.PutUint64(zim[at:at+8], newPos)
	return zim
}

// TestZimExtractorClusterNumOutOfRange covers readZimCluster's
// `num >= hdr.ClusterCount` guard. Build a one-cluster ZIM, then
// patch the dir entry's ClusterNum to 99 so Extract calls
// readZimCluster(.., 99) → out-of-range → continue. With only
// one article, chunks come back nil.
func TestZimExtractorClusterNumOutOfRange(t *testing.T) {
	t.Parallel()
	articles := []zimTestArticle{
		{URL: "x.txt", Mime: "text/plain", Body: []byte("doomed")},
	}
	zim := buildTestZim(t, articles, "text/plain")
	zim = patchDirEntryClusterNum(t, zim, 0, 99)

	chunks, err := NewZimExtractor().Extract(bytes.NewReader(zim), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if chunks != nil {
		t.Errorf("got %d chunks, want nil — out-of-range cluster must skip the article", len(chunks))
	}
}

// TestZimExtractorClusterEndLeqStart covers readZimCluster's
// `if end <= start` guard. Patch the cluster pointer to point
// past ChecksumPos so end (= ChecksumPos for the last cluster)
// is ≤ start (= patched value). Extract continues over the err.
func TestZimExtractorClusterEndLeqStart(t *testing.T) {
	t.Parallel()
	articles := []zimTestArticle{
		{URL: "x.txt", Mime: "text/plain", Body: []byte("doomed")},
	}
	zim := buildTestZim(t, articles, "text/plain")
	hdr := readZimHeaderForTest(t, zim)
	// Force start > ChecksumPos so end-start ≤ 0.
	zim = patchClusterPtrAt(t, zim, 0, hdr.ChecksumPos+1)

	chunks, err := NewZimExtractor().Extract(bytes.NewReader(zim), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if chunks != nil {
		t.Errorf("got %d chunks, want nil — end ≤ start cluster must be rejected", len(chunks))
	}
}

// TestZimExtractorClusterPtrReadFails covers readZimCluster's
// `ra.ReadAt(startBuf[:], …)` err arm. Push the ClusterPtrPos
// in the header beyond the end of the ZIM bytes so the very
// first cluster-ptr ReadAt fails with io.EOF; Extract's outer
// `if err != nil { continue }` swallows it and chunks come back
// nil for the only article.
func TestZimExtractorClusterPtrReadFails(t *testing.T) {
	t.Parallel()
	articles := []zimTestArticle{
		{URL: "x.txt", Mime: "text/plain", Body: []byte("doomed")},
	}
	zim := buildTestZim(t, articles, "text/plain")
	patchHeaderClusterPtrPos(zim, uint64(len(zim))+1024)

	chunks, err := NewZimExtractor().Extract(bytes.NewReader(zim), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if chunks != nil {
		t.Errorf("got %d chunks, want nil — cluster-ptr ReadAt must fail", len(chunks))
	}
}

// TestZimMimeListAllEmpty covers readZimMimeList's
// `if len(mimes) == 0 { return nil, errors.New(...) }` arm.
// Build a ZIM whose mime list is just two consecutive nulls so
// every split-part is empty and the loop produces zero MIMEs.
// The Extract call surfaces a wrapped readZimMimeList err.
func TestZimMimeListAllEmpty(t *testing.T) {
	t.Parallel()
	articles := []zimTestArticle{
		{URL: "x.txt", Mime: "", Body: []byte("doomed")},
	}
	zim := buildTestZim(t, articles, "")

	if _, err := NewZimExtractor().Extract(bytes.NewReader(zim), 0); err == nil {
		t.Error("Extract should fail when the mime list parses to zero MIMEs")
	}
}

// TestZimExtractorClusterSizeExceedsCap covers readZimCluster's
// `size > zimMaxClusterBytes` guard. Patch ChecksumPos far
// beyond clusterPtr+64MiB so end-start > the cap.
func TestZimExtractorClusterSizeExceedsCap(t *testing.T) {
	t.Parallel()
	articles := []zimTestArticle{
		{URL: "x.txt", Mime: "text/plain", Body: []byte("doomed")},
	}
	zim := buildTestZim(t, articles, "text/plain")
	hdr := readZimHeaderForTest(t, zim)
	// Bump ChecksumPos so the implied cluster size exceeds the cap
	// (64 MiB). We don't actually grow the file — readZimCluster
	// rejects the size before any read past EOF.
	patchHeaderChecksumPos(zim, hdr.ChecksumPos+128*1024*1024)

	chunks, err := NewZimExtractor().Extract(bytes.NewReader(zim), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if chunks != nil {
		t.Errorf("got %d chunks, want nil — oversized cluster must be rejected", len(chunks))
	}
}
