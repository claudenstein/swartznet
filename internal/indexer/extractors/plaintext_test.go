package extractors

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"testing/iotest"
)

// TestPlaintextReadAllErr covers Plaintext Extract's
// `io.ReadAll(limited) err → wrap "plaintext: read"` arm.
// iotest.ErrReader fails the post-Peek ReadAll while leaving the
// no-NUL-byte sniff path intact.
func TestPlaintextReadAllErr(t *testing.T) {
	t.Parallel()
	e := NewPlaintextExtractor()
	r := iotest.ErrReader(errors.New("simulated read failure"))
	if _, err := e.Extract(r, 0); err == nil {
		t.Error("Extract should surface ReadAll error from faulty reader")
	}
}

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
	// The exact error string is frozen: the sniff length is however
	// many bytes were actually peeked.
	want := "plaintext: binary signature (NUL byte) detected in first 132 bytes"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
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
	if !strings.ContainsRune(got, '�') {
		t.Errorf("expected U+FFFD replacement, got %q", got)
	}
}

func TestDispatchPicksPlaintext(t *testing.T) {
	t.Parallel()
	// Subtitle formats (.srt, .vtt) are intentionally absent here — they
	// are claimed by SubtitleExtractor instead; see subtitle_test.go.
	// page.html goes to plaintext WITH TAGS LEFT IN: the tag-stripping
	// walker runs only inside EPUB/ZIM, there is no HTML extractor.
	cases := []struct {
		path string
		size int64
		want string
	}{
		{"README.md", 1024, "plaintext"},
		{"src/main.go", 4096, "plaintext"},
		{"dialog.txt", 100, "plaintext"},
		{"config.json", 512, "plaintext"},
		{"page.html", 2048, "plaintext"},
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
	// .mkv and .jpg resolve to video/image MIMEs; plaintext must not
	// claim them. Their metadata extractors (mkv, exif) are deferred,
	// so until those land Dispatch returns nil for both.
	for _, path := range []string{"movie.mkv", "image.jpg"} {
		e, mime := Dispatch(Candidate{Path: path, Size: 2 * 1024 * 1024})
		if e != nil && e.Name() == "plaintext" {
			t.Errorf("plaintext wrongly claimed %s", path)
		}
		if e != nil {
			t.Errorf("%s dispatched to %q (mime=%q), want nil until the deferred extractors land", path, e.Name(), mime)
		}
	}
}

// TestPlaintextClaimsRejectsHugeFile pins the 100 MiB dispatch-time
// size gate on the plaintext claims func.
func TestPlaintextClaimsRejectsHugeFile(t *testing.T) {
	t.Parallel()
	if claimsPlaintext("text/plain", Candidate{Path: "big.txt", Size: 100*1024*1024 + 1}) {
		t.Error("plaintext claimed a file over the 100 MiB gate")
	}
	if !claimsPlaintext("text/plain", Candidate{Path: "ok.txt", Size: 100 * 1024 * 1024}) {
		t.Error("plaintext refused a file at the 100 MiB boundary")
	}
}
