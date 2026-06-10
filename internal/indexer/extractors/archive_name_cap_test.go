package extractors

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"testing"
)

// manyMemberNames returns enough distinct ~93-byte member names to
// push the concatenated name list past the documented 4 MiB cap.
func manyMemberNames() []string {
	const n = 50000 // 50000 × 93 bytes ≈ 4.65 MB > 4 MiB
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("dir/sub/%080d.txt", i)
	}
	return names
}

// TestArchiveTarGzNameListCapEnforced is the regression test for
// the documented-but-unenforced 4 MiB name-list cap: the tar.gz
// branch reads member names from the DECOMPRESSED stream, so before
// the cap a small gzip input could balloon the name list without
// bound. The extractor must stop collecting at the cap and return
// the (large) prefix it gathered.
func TestArchiveTarGzNameListCapEnforced(t *testing.T) {
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	for _, name := range manyMemberNames() {
		if err := tw.WriteHeader(&tar.Header{
			Name:     name,
			Mode:     0o644,
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	var gzBuf bytes.Buffer
	gw, err := gzip.NewWriterLevel(&gzBuf, gzip.BestSpeed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gw.Write(tarBuf.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}

	chunks, err := NewArchiveExtractor().Extract(bytes.NewReader(gzBuf.Bytes()), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if got := len(chunks[0].Text); got > archiveMaxNameBytes {
		t.Fatalf("name list is %d bytes, exceeds the documented %d cap", got, archiveMaxNameBytes)
	}
	// The cap truncates, it must not wipe out the listing entirely.
	if got := len(chunks[0].Text); got < archiveMaxNameBytes/2 {
		t.Fatalf("name list is %d bytes — cap should truncate, not discard", got)
	}
}

// TestArchiveZipNameListCapEnforced pins the same cap on the zip
// branch (bounded by the input buffer, but the documented 4 MiB
// list cap applies all the same).
func TestArchiveZipNameListCapEnforced(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range manyMemberNames() {
		if _, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store}); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	chunks, err := NewArchiveExtractor().Extract(bytes.NewReader(buf.Bytes()), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if got := len(chunks[0].Text); got > archiveMaxNameBytes {
		t.Fatalf("name list is %d bytes, exceeds the documented %d cap", got, archiveMaxNameBytes)
	}
}
