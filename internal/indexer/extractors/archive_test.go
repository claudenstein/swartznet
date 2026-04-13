package extractors

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

func TestArchiveExtractorZip(t *testing.T) {
	t.Parallel()
	// Build a tiny in-memory zip with three files.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"src/main.go", "docs/README.md", "LICENSE"} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte("data")); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
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
	if len(chunks) == 0 {
		t.Fatal("no chunks")
	}
	text := chunks[0].Text
	for _, want := range []string{"src/main.go", "docs/README.md", "LICENSE"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in output:\n%s", want, text)
		}
	}
}

func TestArchiveExtractorTarGz(t *testing.T) {
	t.Parallel()
	// Build a tar.gz with two files.
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	for _, name := range []string{"project/file1.txt", "project/file2.txt"} {
		if err := tw.WriteHeader(&tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     4,
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write([]byte("data")); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}

	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	if _, err := gw.Write(tarBuf.Bytes()); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	e := NewArchiveExtractor()
	chunks, err := e.Extract(bytes.NewReader(gzBuf.Bytes()), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("no chunks")
	}
	text := chunks[0].Text
	if !strings.Contains(text, "file1.txt") || !strings.Contains(text, "file2.txt") {
		t.Errorf("expected file names in output, got:\n%s", text)
	}
}

func TestArchiveExtractorRejectsUnknown(t *testing.T) {
	t.Parallel()
	e := NewArchiveExtractor()
	_, err := e.Extract(strings.NewReader("hello world, not an archive"), 0)
	if err == nil {
		t.Error("expected error for non-archive input")
	}
}

func TestArchiveExtractorDispatchClaimsByExtension(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path  string
		claim bool
	}{
		{"bundle.zip", true},
		{"source.tar.gz", true},
		{"source.tgz", true},
		{"source.tar", true},
		{"photo.jpg", false},
		{"doc.pdf", false},
	}
	for _, tc := range cases {
		got, _ := Dispatch(Candidate{Path: tc.path, Size: 1024})
		isArchive := got != nil && got.Name() == "archive"
		if isArchive != tc.claim {
			t.Errorf("%s: got claim=%v, want %v", tc.path, isArchive, tc.claim)
		}
	}
}
