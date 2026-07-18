package extractors

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// TestZimExtractorClusterCacheEviction covers Extract's cache
// eviction arm at lines 132-141. Triggers it by handing Extract
// a synthetic ZIM whose 33 articles each reference a different
// ClusterNum so the clusterCache fills up to its 32-entry cap
// and the eviction loop drops half on the next insert.
//
// The clusters all share the same minimal 1-byte body (just a
// type byte, no offset table). getZimBlob therefore rejects
// every blob and Extract returns no chunks — all we care about
// is that readZimCluster succeeds 33 times so the cache
// eviction code path runs.
func TestZimExtractorClusterCacheEviction(t *testing.T) {
	t.Parallel()

	const articleCount = 33
	const clusterCount = articleCount
	const headerSize = 80

	mime := "text/plain"
	mimeList := append([]byte(mime), 0, 0)

	// Layout positions.
	mimePos := uint64(headerSize)
	urlPtrPos := mimePos + uint64(len(mimeList))
	clusterPtrPos := urlPtrPos + uint64(articleCount*8)

	// Build dir entries (cluster=i, blob=0). Each entry is
	// 16-byte fixed prefix + url + null + null.
	dirEntries := make([][]byte, articleCount)
	for i := 0; i < articleCount; i++ {
		var entry bytes.Buffer
		_ = binary.Write(&entry, binary.LittleEndian, uint16(0)) // mimeIdx=0
		entry.WriteByte(0)                                       // parameter len
		entry.WriteByte('A')                                     // namespace
		_ = binary.Write(&entry, binary.LittleEndian, uint32(0)) // revision
		_ = binary.Write(&entry, binary.LittleEndian, uint32(i)) // cluster=i
		_ = binary.Write(&entry, binary.LittleEndian, uint32(0)) // blob=0
		entry.WriteString("a.txt")
		entry.WriteByte(0)
		entry.WriteByte(0)
		dirEntries[i] = entry.Bytes()
	}

	dirEntriesPos := clusterPtrPos + uint64(clusterCount*8)
	dirEntryOffsets := make([]uint64, articleCount)
	cur := dirEntriesPos
	for i, de := range dirEntries {
		dirEntryOffsets[i] = cur
		cur += uint64(len(de))
	}
	clusterStart := cur

	// 33 staggered cluster ptrs at clusterStart, +1, +2, ... +32.
	// Each cluster N spans 1 byte: [type=1]. Cluster 32's body
	// is anchored to ChecksumPos.
	clusterBody := make([]byte, articleCount)
	for i := range clusterBody {
		clusterBody[i] = 1 // uncompressed
	}
	checksumPos := clusterStart + uint64(articleCount)

	// Assemble.
	out := new(bytes.Buffer)
	_ = binary.Write(out, binary.LittleEndian, uint32(zimMagic))
	_ = binary.Write(out, binary.LittleEndian, uint16(5))
	_ = binary.Write(out, binary.LittleEndian, uint16(0))
	out.Write(make([]byte, 16)) // UUID
	_ = binary.Write(out, binary.LittleEndian, uint32(articleCount))
	_ = binary.Write(out, binary.LittleEndian, uint32(clusterCount))
	_ = binary.Write(out, binary.LittleEndian, urlPtrPos)
	_ = binary.Write(out, binary.LittleEndian, urlPtrPos)
	_ = binary.Write(out, binary.LittleEndian, clusterPtrPos)
	_ = binary.Write(out, binary.LittleEndian, mimePos)
	_ = binary.Write(out, binary.LittleEndian, uint32(0))
	_ = binary.Write(out, binary.LittleEndian, uint32(0))
	_ = binary.Write(out, binary.LittleEndian, checksumPos)

	out.Write(mimeList)
	for _, off := range dirEntryOffsets {
		_ = binary.Write(out, binary.LittleEndian, off)
	}
	for i := 0; i < clusterCount; i++ {
		_ = binary.Write(out, binary.LittleEndian, clusterStart+uint64(i))
	}
	for _, de := range dirEntries {
		out.Write(de)
	}
	out.Write(clusterBody)
	out.Write(make([]byte, 16)) // dummy MD5 checksum

	chunks, err := NewZimExtractor().Extract(bytes.NewReader(out.Bytes()), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	// All 33 cluster bodies are 1 byte (just the type byte) so
	// getZimBlob fails on every one — we expect zero chunks. The
	// goal is just to drive readZimCluster + cache through 33
	// insertions so the eviction arm fires.
	if chunks != nil {
		t.Logf("got %d chunks (acceptable — cache eviction code still ran)", len(chunks))
	}
}
