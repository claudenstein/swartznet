package extractors

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// TestGetZimBlobEmptyCluster covers the
// `if len(cluster) < 1` arm.
func TestGetZimBlobEmptyCluster(t *testing.T) {
	t.Parallel()
	if _, err := getZimBlob(nil, 0); err == nil {
		t.Error("getZimBlob(nil) should error")
	}
}

// TestGetZimBlobBodyShorterThanFirstOffset covers the
// `if len(body) < offsetSize` arm. A cluster whose body is
// shorter than the offset table can't be parsed.
func TestGetZimBlobBodyShorterThanFirstOffset(t *testing.T) {
	t.Parallel()
	// type=1 (uncompressed, non-extended), body of 2 bytes
	// (less than the 4-byte offsetSize).
	cluster := []byte{0x01, 0xAA, 0xBB}
	if _, err := getZimBlob(cluster, 0); err == nil {
		t.Error("getZimBlob with short body should error")
	}
}

// TestGetZimBlobBadFirstOffset covers the
// `firstOffset == 0 || firstOffset%offsetSize != 0` arm.
func TestGetZimBlobBadFirstOffset(t *testing.T) {
	t.Parallel()
	// type=1, body has firstOffset = 5 (not divisible by 4).
	body := make([]byte, 8)
	binary.LittleEndian.PutUint32(body[:4], 5)
	cluster := append([]byte{0x01}, body...)
	if _, err := getZimBlob(cluster, 0); err == nil {
		t.Error("getZimBlob with non-aligned first offset should error")
	}
}

// TestGetZimBlobBlobOutOfRange covers the
// `if uint64(blobNum) >= blobCount` arm. firstOffset=4 means
// 1 entry (just the sentinel), so 0 blobs.
func TestGetZimBlobBlobOutOfRange(t *testing.T) {
	t.Parallel()
	body := make([]byte, 4)
	binary.LittleEndian.PutUint32(body[:4], 4) // 1 entry → 0 blobs
	cluster := append([]byte{0x01}, body...)
	if _, err := getZimBlob(cluster, 0); err == nil {
		t.Error("getZimBlob asking for blob 0 in 0-blob cluster should error")
	}
}

// TestGetZimBlobOffsetEntryPastClusterEnd covers the
// `if eIdx+offsetSize > len(body)` arm.
func TestGetZimBlobOffsetEntryPastClusterEnd(t *testing.T) {
	t.Parallel()
	// firstOffset=12 → 3 entries → 2 blobs claimed. But only
	// 8 body bytes total — the second offset entry is past the
	// end.
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint32(12))
	binary.Write(&buf, binary.LittleEndian, uint32(99)) // bogus second
	cluster := append([]byte{0x01}, buf.Bytes()...)
	if _, err := getZimBlob(cluster, 1); err == nil {
		t.Error("getZimBlob with offset entry past cluster end should error")
	}
}

// TestGetZimBlobInvalidOffsets covers the
// `if startOff > endOff || endOff > uint64(len(body))` arm.
func TestGetZimBlobInvalidOffsets(t *testing.T) {
	t.Parallel()
	// firstOffset=12 → 3 entries. offsets: [12, 100, 200].
	// blob 0 spans [12, 100] but body is much shorter.
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint32(12))
	binary.Write(&buf, binary.LittleEndian, uint32(100))
	binary.Write(&buf, binary.LittleEndian, uint32(200))
	cluster := append([]byte{0x01}, buf.Bytes()...)
	if _, err := getZimBlob(cluster, 0); err == nil {
		t.Error("getZimBlob with end past body should error")
	}
}

// TestGetZimBlobExtendedClusterUsesEightByteOffsets covers the
// extended-cluster path where offsets are 8 bytes wide. Build
// an extended cluster with one valid blob and verify it slices
// out cleanly.
func TestGetZimBlobExtendedClusterUsesEightByteOffsets(t *testing.T) {
	t.Parallel()
	// type=0x11 (uncompressed + extended bit set).
	const blob = "extended-blob-content"
	var body bytes.Buffer
	// firstOffset = 16 (2 entries × 8 bytes)
	binary.Write(&body, binary.LittleEndian, uint64(16))
	binary.Write(&body, binary.LittleEndian, uint64(16+uint64(len(blob))))
	body.WriteString(blob)

	cluster := append([]byte{0x11}, body.Bytes()...)
	got, err := getZimBlob(cluster, 0)
	if err != nil {
		t.Fatalf("getZimBlob: %v", err)
	}
	if string(got) != blob {
		t.Errorf("blob = %q, want %q", got, blob)
	}
}
