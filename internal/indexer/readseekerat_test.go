package indexer_test

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/swartznet/swartznet/internal/indexer"
)

// seekOnly exposes ONLY Read and Seek from a *bytes.Reader — the shape of
// an anacrolix torrent reader minus Close.
type seekOnly struct{ r *bytes.Reader }

func (s *seekOnly) Read(p []byte) (int, error) { return s.r.Read(p) }
func (s *seekOnly) Seek(off int64, whence int) (int64, error) {
	return s.r.Seek(off, whence)
}

// seekCloser adds a Close-recording flag on top of seekOnly.
type seekCloser struct {
	seekOnly
	closed bool
}

func (s *seekCloser) Close() error {
	s.closed = true
	return nil
}

func TestReadSeekerAtReadAt(t *testing.T) {
	t.Parallel()
	const body = "0123456789abcdef"
	ra := indexer.NewReadSeekerAt(&seekOnly{r: bytes.NewReader([]byte(body))}, int64(len(body)))

	// Full read in the middle.
	buf := make([]byte, 4)
	n, err := ra.ReadAt(buf, 10)
	if n != 4 || err != nil {
		t.Fatalf("ReadAt(mid) = %d, %v; want 4, nil", n, err)
	}
	if string(buf) != "abcd" {
		t.Errorf("ReadAt(mid) read %q, want %q", buf, "abcd")
	}

	// Exactly-at-end full read: err may be nil or io.EOF per the
	// io.ReaderAt contract; n must be len(p).
	n, err = ra.ReadAt(buf, int64(len(body)-4))
	if n != 4 || (err != nil && err != io.EOF) {
		t.Fatalf("ReadAt(tail) = %d, %v; want 4 and nil-or-EOF", n, err)
	}

	// Partial read past the end must return io.EOF.
	n, err = ra.ReadAt(buf, int64(len(body)-2))
	if n != 2 || err != io.EOF {
		t.Fatalf("ReadAt(past end) = %d, %v; want 2, io.EOF", n, err)
	}
}

// TestReadSeekerAtPreservesSequentialPosition pins the io.ReaderAt
// contract clause "ReadAt should not affect the underlying seek offset":
// an interleaved ReadAt must leave a sequential Read stream unbroken.
func TestReadSeekerAtPreservesSequentialPosition(t *testing.T) {
	t.Parallel()
	ra := indexer.NewReadSeekerAt(&seekOnly{r: bytes.NewReader([]byte("hello world"))}, 11)

	first := make([]byte, 5)
	if _, err := io.ReadFull(ra, first); err != nil {
		t.Fatal(err)
	}
	if _, err := ra.ReadAt(make([]byte, 3), 0); err != nil {
		t.Fatal(err)
	}
	rest, err := io.ReadAll(ra)
	if err != nil {
		t.Fatal(err)
	}
	if string(first)+string(rest) != "hello world" {
		t.Errorf("sequential stream corrupted by ReadAt: %q + %q", first, rest)
	}
}

func TestReadSeekerAtConcurrentReadAt(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("0123456789", 100)
	ra := indexer.NewReadSeekerAt(&seekOnly{r: bytes.NewReader([]byte(body))}, int64(len(body)))

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(off int64) {
			defer wg.Done()
			buf := make([]byte, 10)
			for i := 0; i < 50; i++ {
				n, err := ra.ReadAt(buf, off)
				if n != 10 || err != nil {
					t.Errorf("ReadAt(%d) = %d, %v", off, n, err)
					return
				}
				if string(buf) != "0123456789" {
					t.Errorf("ReadAt(%d) read %q — cursor race", off, buf)
					return
				}
			}
		}(int64(g * 10))
	}
	wg.Wait()
}

func TestReadSeekerAtClosePassThrough(t *testing.T) {
	t.Parallel()
	sc := &seekCloser{seekOnly: seekOnly{r: bytes.NewReader([]byte("x"))}}
	ra := indexer.NewReadSeekerAt(sc, 1)
	if err := ra.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !sc.closed {
		t.Error("Close did not pass through to the underlying io.Closer")
	}

	// Non-closer underlying reader: Close is a no-op, never an error.
	plain := indexer.NewReadSeekerAt(&seekOnly{r: bytes.NewReader([]byte("y"))}, 1)
	if err := plain.Close(); err != nil {
		t.Errorf("Close on non-closer = %v, want nil", err)
	}
}
