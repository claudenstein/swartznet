package extractors

import (
	"bytes"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// TestReadZimClusterZstdHappyPath covers readZimCluster's
// `case zimCompZstd: …` success arm at lines 361-364. Calling
// readZimCluster directly with a synthetic ReaderAt + hand-built
// zimHeader lets us put the cluster bytes at exactly the size we
// want — a single zstd frame, no trailing padding — so DecodeAll
// succeeds and the prepend-typeByte success path runs.
func TestReadZimClusterZstdHappyPath(t *testing.T) {
	t.Parallel()

	// Compress some payload so the zstd frame size is known.
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("hello zstd world")
	frame := enc.EncodeAll(payload, nil)
	enc.Close()

	// Cluster bytes = [type=5 (zstd)] || frame, exactly len(frame)+1.
	cluster := append([]byte{5}, frame...)

	// Synthetic file = cluster bytes positioned at offset 0; checksum
	// just past the cluster. Pointer table at the end.
	clusterStart := 0
	clusterEnd := len(cluster)
	checksumPos := clusterEnd
	clusterPtrPos := checksumPos // ptr table after checksum (just for layout)

	// Build the file. ReadAt only needs the ranges we read:
	//   - [clusterStart, clusterEnd) → cluster bytes
	//   - clusterPtrPos → uint64 LE = clusterStart
	file := make([]byte, clusterPtrPos+8)
	copy(file[clusterStart:clusterEnd], cluster)
	// uint64 LE clusterStart at clusterPtrPos:
	for i := 0; i < 8; i++ {
		file[clusterPtrPos+i] = byte(uint64(clusterStart) >> (i * 8))
	}

	hdr := &zimHeader{
		ClusterCount:  1,
		ClusterPtrPos: uint64(clusterPtrPos),
		ChecksumPos:   uint64(checksumPos),
	}

	out, err := readZimCluster(bytes.NewReader(file), hdr, 0)
	if err != nil {
		t.Fatalf("readZimCluster: %v", err)
	}
	// Output is [typeByte || decompressed-payload]. The success arm
	// prepends typeByte=5; payload follows verbatim.
	if len(out) == 0 || out[0] != 5 {
		t.Fatalf("expected typeByte 5, got %v", out)
	}
	if !bytes.Equal(out[1:], payload) {
		t.Errorf("decompressed payload = %q, want %q", out[1:], payload)
	}
}
