package wirecompat

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"
)

// readFile is os.ReadFile, named for test readability.
func readFile(path string) ([]byte, error) { return os.ReadFile(path) }

// buildTestZimBlob synthesizes a minimal valid ZIM file with a single
// uncompressed text/plain article carrying body. This is a self-contained
// copy of the extractors package's test synthesizer so the Layer-L wiring
// test can seed a real .zim without coupling to that package's test scope.
// The ZIM format is frozen (major version 5).
func buildTestZimBlob(t *testing.T, body string) []byte {
	t.Helper()
	const zimMagic uint32 = 0x44D495A // little-endian "ZIM\x04"
	const headerSize = 80
	const mime0 = "text/plain"

	blob := []byte(body)
	mimeList := append([]byte(mime0), 0, 0)

	// Cluster body: uint32-LE offset table (blobCount+1) then the blob.
	offsetTableBytes := 2 * 4
	ot := new(bytes.Buffer)
	_ = binary.Write(ot, binary.LittleEndian, uint32(offsetTableBytes))
	_ = binary.Write(ot, binary.LittleEndian, uint32(offsetTableBytes+len(blob)))
	clusterBody := append(ot.Bytes(), blob...)
	cluster := append([]byte{1}, clusterBody...) // type 1, uncompressed

	mimePos := uint64(headerSize)
	urlPtrPos := mimePos + uint64(len(mimeList))
	clusterPtrPos := urlPtrPos + 8 // one URL ptr

	var entry bytes.Buffer
	_ = binary.Write(&entry, binary.LittleEndian, uint16(0)) // mimeIdx
	entry.WriteByte(0)                                       // parameter len
	entry.WriteByte('A')                                     // namespace
	_ = binary.Write(&entry, binary.LittleEndian, uint32(0)) // revision
	_ = binary.Write(&entry, binary.LittleEndian, uint32(0)) // cluster
	_ = binary.Write(&entry, binary.LittleEndian, uint32(0)) // blob
	entry.WriteString("A/article")
	entry.WriteByte(0)
	entry.WriteByte(0) // empty title + terminator
	dirEntry := entry.Bytes()

	dirEntriesPos := clusterPtrPos + 8
	clusterPos := dirEntriesPos + uint64(len(dirEntry))
	checksumPos := clusterPos + uint64(len(cluster))

	out := new(bytes.Buffer)
	_ = binary.Write(out, binary.LittleEndian, zimMagic)
	_ = binary.Write(out, binary.LittleEndian, uint16(5)) // major version
	_ = binary.Write(out, binary.LittleEndian, uint16(0))
	out.Write(make([]byte, 16))                           // UUID
	_ = binary.Write(out, binary.LittleEndian, uint32(1)) // articleCount
	_ = binary.Write(out, binary.LittleEndian, uint32(1)) // clusterCount
	_ = binary.Write(out, binary.LittleEndian, urlPtrPos)
	_ = binary.Write(out, binary.LittleEndian, urlPtrPos) // titlePtrPos
	_ = binary.Write(out, binary.LittleEndian, clusterPtrPos)
	_ = binary.Write(out, binary.LittleEndian, mimePos)
	_ = binary.Write(out, binary.LittleEndian, uint32(0)) // mainPage
	_ = binary.Write(out, binary.LittleEndian, uint32(0)) // layoutPage
	_ = binary.Write(out, binary.LittleEndian, checksumPos)

	out.Write(mimeList)
	_ = binary.Write(out, binary.LittleEndian, dirEntriesPos) // URL ptr
	_ = binary.Write(out, binary.LittleEndian, clusterPos)    // cluster ptr
	out.Write(dirEntry)
	out.Write(cluster)
	out.Write(make([]byte, 16)) // dummy checksum
	return out.Bytes()
}
