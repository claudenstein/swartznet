package extractors

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
)

// seekOnlyReader exposes ONLY Read and Seek from an underlying
// *bytes.Reader — the exact shape of a torrent file reader, which
// implements Read/Seek/Close but NOT io.ReaderAt.
type seekOnlyReader struct{ r *bytes.Reader }

func (s *seekOnlyReader) Read(p []byte) (int, error) { return s.r.Read(p) }
func (s *seekOnlyReader) Seek(off int64, whence int) (int64, error) {
	return s.r.Seek(off, whence)
}

// seekReaderAt adapts an io.ReadSeeker into an io.Reader+io.ReaderAt
// by serializing Seek+ReadFull under a mutex — the same shape as the
// engine's ReadSeekerAt shim that feeds the ZIM extractor from the
// live pipeline (the seek cursor is shared state, so ReadAt must be
// mutex-serialized to stay stateless for callers).
type seekReaderAt struct {
	mu sync.Mutex
	rs io.ReadSeeker
}

func (s *seekReaderAt) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rs.Read(p)
}

func (s *seekReaderAt) ReadAt(p []byte, off int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.rs.Seek(off, io.SeekStart); err != nil {
		return 0, err
	}
	return io.ReadFull(s.rs, p)
}

// TestZimExtractorThroughSeekerBackedReaderAt proves the ReaderAt
// requirement is satisfiable by a NON-bytes.Reader path: a
// seeker-only stream wrapped in a Seek-based ReadAt adapter must
// extract identically to the direct bytes.Reader path. This is the
// shape the engine's ReadSeekerAt shim delivers in the live
// pipeline (legacy fed the extractor a bare torrent reader, which
// failed the type assertion on every daemon-fed .zim).
func TestZimExtractorThroughSeekerBackedReaderAt(t *testing.T) {
	t.Parallel()

	articles := []zimTestArticle{
		{URL: "a.html", Mime: "text/html", Body: []byte("<html><body><p>alpha article body</p></body></html>")},
		{URL: "b.html", Mime: "text/html", Body: []byte("<html><body><p>beta article body</p></body></html>")},
	}
	zim := buildTestZim(t, articles, "text/html")

	// The inner stream must NOT itself be an io.ReaderAt, or the
	// test would silently degrade to the bytes.Reader path.
	inner := &seekOnlyReader{r: bytes.NewReader(zim)}
	if _, ok := interface{}(inner).(io.ReaderAt); ok {
		t.Fatal("seekOnlyReader must not implement io.ReaderAt")
	}

	shim := &seekReaderAt{rs: inner}
	got, err := NewZimExtractor().Extract(shim, 0)
	if err != nil {
		t.Fatalf("Extract through seeker-backed ReaderAt: %v", err)
	}

	want, err := NewZimExtractor().Extract(bytes.NewReader(zim), 0)
	if err != nil {
		t.Fatalf("Extract through bytes.Reader: %v", err)
	}

	if len(got) != len(want) {
		t.Fatalf("chunk count %d through shim, %d through bytes.Reader", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("chunk %d differs: shim %+v, direct %+v", i, got[i], want[i])
		}
	}
	joined := ""
	for _, c := range got {
		joined += c.Text + "\n"
	}
	for _, wantText := range []string{"alpha article body", "beta article body"} {
		if !strings.Contains(joined, wantText) {
			t.Errorf("missing %q in shim-path output:\n%s", wantText, joined)
		}
	}
}
