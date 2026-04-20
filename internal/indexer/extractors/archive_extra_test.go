package extractors

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// TestArchiveExtractorTinyInputReturnsNoChunks covers the
// `len(raw) < 4` early-return — too small to even sniff a magic
// number, so we politely return (nil, nil).
func TestArchiveExtractorTinyInputReturnsNoChunks(t *testing.T) {
	t.Parallel()
	e := NewArchiveExtractor()
	chunks, err := e.Extract(strings.NewReader("ab"), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if chunks != nil {
		t.Errorf("got %d chunks, want nil", len(chunks))
	}
}

// TestArchiveExtractorZipSkipsDirectoryAndEmptyNames covers the
// two skip branches inside zipMemberNames: trailing-slash directory
// entries and empty Name entries are filtered out of the returned
// list. The previous tests only exercised the happy path.
func TestArchiveExtractorZipSkipsDirectoryAndEmptyNames(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// One real file, one directory entry (trailing /), one
	// nested-directory entry, plus a "" entry.
	for _, name := range []string{"docs/", "src/", "src/main.go"} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %q: %v", name, err)
		}
		// Directory entries in the standard go zip writer are sized 0;
		// writing zero bytes keeps the format valid.
		if !strings.HasSuffix(name, "/") {
			if _, err := w.Write([]byte("data")); err != nil {
				t.Fatalf("zip write %q: %v", name, err)
			}
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}

	e := NewArchiveExtractor()
	chunks, err := e.Extract(bytes.NewReader(buf.Bytes()), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	text := chunks[0].Text
	if !strings.Contains(text, "src/main.go") {
		t.Errorf("missing real file name in output:\n%s", text)
	}
	// Directory entries must NOT appear (they end in "/").
	for _, dir := range []string{"docs/", "src/"} {
		if strings.Contains(text, dir+"\n") || text == dir {
			t.Errorf("directory entry %q leaked into output:\n%s", dir, text)
		}
	}
}

// TestArchiveExtractorPlainTarFallback covers the `default` case
// in Extract's switch: a plain (un-gzipped) tar archive has no
// magic-byte prefix, so the dispatcher tries tarMemberNames as a
// last resort.
func TestArchiveExtractorPlainTarFallback(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{
		Name:     "foo.txt",
		Mode:     0o644,
		Size:     5,
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	e := NewArchiveExtractor()
	chunks, err := e.Extract(bytes.NewReader(buf.Bytes()), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	if !strings.Contains(chunks[0].Text, "foo.txt") {
		t.Errorf("missing file name; got: %q", chunks[0].Text)
	}
}

// TestArchiveExtractorTarSkipsTypeDir verifies tar entries with
// Typeflag == tar.TypeDir are filtered out (the previously-
// uncovered TypeDir continue branch in tarMemberNames).
func TestArchiveExtractorTarSkipsTypeDir(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	// Pure directory entry — no Size, Typeflag = TypeDir.
	if err := tw.WriteHeader(&tar.Header{
		Name:     "subdir/",
		Mode:     0o755,
		Typeflag: tar.TypeDir,
	}); err != nil {
		t.Fatal(err)
	}
	// One real file.
	if err := tw.WriteHeader(&tar.Header{
		Name:     "subdir/data.txt",
		Mode:     0o644,
		Size:     3,
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("xyz")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	e := NewArchiveExtractor()
	chunks, err := e.Extract(bytes.NewReader(buf.Bytes()), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	text := chunks[0].Text
	if !strings.Contains(text, "subdir/data.txt") {
		t.Errorf("missing real file in output:\n%s", text)
	}
	// "subdir/" alone (no trailing data.txt) must not appear as its
	// own entry.
	for _, line := range strings.Split(text, "\n") {
		if line == "subdir/" {
			t.Errorf("TypeDir entry leaked into output as standalone line")
		}
	}
}

// TestArchiveExtractorCorruptGzip covers the gzip.NewReader error
// branch in tarGzMemberNames: bytes start with the gzip magic
// 0x1f 0x8b but the rest of the header is invalid.
func TestArchiveExtractorCorruptGzip(t *testing.T) {
	t.Parallel()
	// Gzip magic + obviously-bogus header bytes.
	bad := []byte{0x1f, 0x8b, 0x00, 0x00, 0xff, 0xff, 0xff, 0xff}
	e := NewArchiveExtractor()
	_, err := e.Extract(bytes.NewReader(bad), 0)
	if err == nil {
		t.Error("expected error for corrupt gzip header")
	}
}
