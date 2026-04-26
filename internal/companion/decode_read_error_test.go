package companion_test

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"testing"

	"github.com/swartznet/swartznet/internal/companion"
)

// gzipPrefixThenError starts a valid gzip stream so
// gzip.NewReader's header validation passes, then errors on
// every subsequent Read so io.ReadAll inside Decode surfaces
// the wrapped error.
type gzipPrefixThenError struct {
	prefix []byte
	pos    int
}

func (g *gzipPrefixThenError) Read(p []byte) (int, error) {
	if g.pos < len(g.prefix) {
		n := copy(p, g.prefix[g.pos:])
		g.pos += n
		return n, nil
	}
	return 0, errors.New("companion test: forced read failure")
}

// TestDecodeReadAllError covers Decode's
// `body, err := io.ReadAll(io.LimitReader(gz, ...))` error arm.
// gzip.NewReader successfully reads the magic + header, then
// the underlying reader errors on subsequent reads. Decode must
// surface the wrapped 'read gzip' error rather than panic.
func TestDecodeReadAllError(t *testing.T) {
	t.Parallel()
	// Build a real gzip header (10 bytes), enough for
	// gzip.NewReader to succeed but not enough to decode any
	// real content.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write([]byte("partial")); err != nil {
		t.Fatal(err)
	}
	// Don't close — keep the stream incomplete.

	r := &gzipPrefixThenError{prefix: buf.Bytes()}
	if _, err := companion.Decode(r); err == nil {
		t.Error("Decode should surface read error")
	}
}

// satisfy io import.
var _ io.Reader = (*gzipPrefixThenError)(nil)
