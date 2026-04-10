package extractors

import (
	"bytes"
	"strings"
	"testing"
)

func TestPlaintextExtractsSimpleUTF8(t *testing.T) {
	t.Parallel()
	e := NewPlaintextExtractor()
	src := strings.Repeat("The quick brown fox jumps over the lazy dog.\n", 5)
	chunks, err := e.Extract(strings.NewReader(src), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("len(chunks) = %d, want 1", len(chunks))
	}
	if chunks[0].Text != src {
		t.Errorf("chunks[0].Text mismatch")
	}
}

func TestPlaintextRejectsBinary(t *testing.T) {
	t.Parallel()
	e := NewPlaintextExtractor()
	// ELF magic + NUL bytes = clearly not text.
	binary := append([]byte{0x7f, 'E', 'L', 'F'}, make([]byte, 128)...)
	_, err := e.Extract(bytes.NewReader(binary), 0)
	if err == nil {
		t.Fatal("expected error for binary input, got nil")
	}
	if !strings.Contains(err.Error(), "NUL") {
		t.Errorf("expected NUL-byte error, got %q", err.Error())
	}
}

func TestPlaintextStripsBOM(t *testing.T) {
	t.Parallel()
	e := NewPlaintextExtractor()
	src := []byte{0xef, 0xbb, 0xbf}
	src = append(src, []byte("hello world")...)
	chunks, err := e.Extract(bytes.NewReader(src), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 || chunks[0].Text != "hello world" {
		t.Errorf("BOM not stripped; got %q", chunks[0].Text)
	}
}

func TestPlaintextEmptyFile(t *testing.T) {
	t.Parallel()
	e := NewPlaintextExtractor()
	chunks, err := e.Extract(strings.NewReader("   \n \t "), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 0 {
		t.Errorf("len(chunks) = %d, want 0 for whitespace-only input", len(chunks))
	}
}

func TestPlaintextSanitizesInvalidUTF8(t *testing.T) {
	t.Parallel()
	e := NewPlaintextExtractor()
	// "abc" + invalid 0xff + "def" — the 0xff becomes U+FFFD.
	src := append([]byte("abc"), 0xff, 'd', 'e', 'f')
	chunks, err := e.Extract(bytes.NewReader(src), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 {
		t.Fatalf("len(chunks) = %d", len(chunks))
	}
	got := chunks[0].Text
	if !strings.Contains(got, "abc") || !strings.Contains(got, "def") {
		t.Errorf("lost surrounding characters: %q", got)
	}
	if !strings.ContainsRune(got, '\uFFFD') {
		t.Errorf("expected U+FFFD replacement, got %q", got)
	}
}

func TestDispatchPicksPlaintext(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path string
		size int64
		want string
	}{
		{"README.md", 1024, "plaintext"},
		{"movie.en.srt", 32 * 1024, "plaintext"},
		{"src/main.go", 4096, "plaintext"},
		{"subtitles.vtt", 8192, "plaintext"},
		{"dialog.txt", 100, "plaintext"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			e, mime := Dispatch(Candidate{Path: tc.path, Size: tc.size})
			if e == nil {
				t.Fatalf("no extractor dispatched for %s (mime=%q)", tc.path, mime)
			}
			if e.Name() != tc.want {
				t.Errorf("extractor = %s, want %s", e.Name(), tc.want)
			}
		})
	}
}

func TestDispatchRefusesBinary(t *testing.T) {
	t.Parallel()
	e, _ := Dispatch(Candidate{Path: "movie.mkv", Size: 5 * 1024 * 1024 * 1024})
	if e != nil {
		t.Errorf("plaintext claimed .mkv file; Dispatch returned %s", e.Name())
	}
	e, _ = Dispatch(Candidate{Path: "image.jpg", Size: 2 * 1024 * 1024})
	if e != nil {
		t.Errorf("plaintext claimed .jpg file; Dispatch returned %s", e.Name())
	}
}
