package indexer

import (
	"io"
	"sync"
)

// ReadSeekerAt adapts an io.ReadSeeker into a size-bounded io.ReaderAt so
// ReaderAt-requiring extractors (ZIM) work against readers that only expose
// Read/Seek/Close — the exact shape of an anacrolix torrent reader, which is
// why every daemon-fed .zim silently failed in legacy (SPEC §6.1).
//
// The size bound is load-bearing for multi-file torrents: anacrolix's
// File.NewReader().Read reads up to len(buf) bytes from storage and only sets
// io.EOF once the position passes the file's end — so a buffer larger than
// the file over-reads into the following files' bytes. Clamping every Read
// and ReadAt to size confines each extractor to its own file.
//
// The seek cursor is shared state, so every method serializes on one mutex:
// concurrent ReadAt callers are safe, and ReadAt neither affects nor is
// affected by the sequential Read position (it restores the cursor before
// returning). Close passes through to the underlying reader when it
// implements io.Closer and must still be called ONLY from the extracting
// goroutine after Extract returns — the shim serializes Read vs ReadAt, not
// Read vs Close.
type ReadSeekerAt struct {
	mu   sync.Mutex
	rs   io.ReadSeeker
	size int64
	pos  int64 // logical read position, for Read bounding
}

// NewReadSeekerAt wraps rs, bounding all reads to size bytes (the file's
// logical length). The caller must not use rs directly while the shim is
// alive.
func NewReadSeekerAt(rs io.ReadSeeker, size int64) *ReadSeekerAt {
	return &ReadSeekerAt{rs: rs, size: size}
}

// Read reads sequentially, never past the size bound.
func (r *ReadSeekerAt) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pos >= r.size {
		return 0, io.EOF
	}
	if remain := r.size - r.pos; int64(len(p)) > remain {
		p = p[:remain]
	}
	n, err := r.rs.Read(p)
	r.pos += int64(n)
	return n, err
}

// Seek repositions the underlying reader and the logical bound. The
// underlying anacrolix reader's window is already file-relative, so the
// offsets match the shim's size window.
func (r *ReadSeekerAt) Seek(offset int64, whence int) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	newPos, err := r.rs.Seek(offset, whence)
	if err == nil {
		r.pos = newPos
	}
	return newPos, err
}

// ReadAt implements io.ReaderAt: it fills p from offset off within the size
// window, returning len(p) bytes or the error that stopped it (io.EOF at end
// of the file). The sequential position is saved and restored around the
// read.
func (r *ReadSeekerAt) ReadAt(p []byte, off int64) (n int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if off >= r.size {
		return 0, io.EOF
	}
	if remain := r.size - off; int64(len(p)) > remain {
		p = p[:remain]
		defer func() {
			// A clamped short read still reports EOF to the caller.
			if err == nil {
				err = io.EOF
			}
		}()
	}

	cur, serr := r.rs.Seek(0, io.SeekCurrent)
	if serr != nil {
		return 0, serr
	}
	if _, serr := r.rs.Seek(off, io.SeekStart); serr != nil {
		return 0, serr
	}
	for n < len(p) && err == nil {
		var m int
		m, err = r.rs.Read(p[n:])
		n += m
	}
	if _, seekErr := r.rs.Seek(cur, io.SeekStart); seekErr != nil && err == nil {
		err = seekErr
	}
	if n == len(p) && err == io.EOF {
		err = nil
	}
	return n, err
}

// Close closes the underlying reader when it implements io.Closer; otherwise
// it is a no-op.
func (r *ReadSeekerAt) Close() error {
	if c, ok := r.rs.(io.Closer); ok {
		return c.Close()
	}
	return nil
}
