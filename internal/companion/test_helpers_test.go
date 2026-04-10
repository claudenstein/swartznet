package companion_test

import (
	"compress/gzip"
	"io"
)

// gzipWriterFor is a tiny shim around gzip.NewWriter that the
// test files use to synthesise gzip-framed bad-format payloads
// for the negative-path tests, without leaking gzip imports
// into every test file.
func gzipWriterFor(w io.Writer) (*gzip.Writer, error) {
	return gzip.NewWriter(w), nil
}
