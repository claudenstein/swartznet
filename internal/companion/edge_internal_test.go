package companion

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAtomicWriteOpenFileError — when the parent directory
// doesn't exist, OpenFile fails immediately. Covers the first
// error branch in atomicWrite (the one not exercised by the
// existing happy-path tests).
func TestAtomicWriteOpenFileError(t *testing.T) {
	t.Parallel()
	bogus := filepath.Join(t.TempDir(), "does-not-exist", "child", "out.bin")
	if err := atomicWrite(bogus, []byte("data")); err == nil {
		t.Error("atomicWrite into missing parent dir should error")
	}
}

// TestBytesPageSourceNumPiecesZeroPieceSize — when PieceSize is
// not positive, NumPieces returns 0 rather than dividing by
// zero. This guard matters because callers loop up to
// NumPieces() and would panic on a malformed source otherwise.
func TestBytesPageSourceNumPiecesZeroPieceSize(t *testing.T) {
	t.Parallel()
	for _, ps := range []int{0, -1, -16384} {
		s := &BytesPageSource{Data: make([]byte, 1024), PieceSize: ps}
		if got := s.NumPieces(); got != 0 {
			t.Errorf("PieceSize=%d → NumPieces=%d, want 0", ps, got)
		}
	}
}

// TestBytesPageSourcePieceOOR — Piece(idx) for idx ≥ NumPieces
// returns an "out of range" error rather than slicing past the
// end of Data. This is the guard the leaf-fetch loop relies on.
func TestBytesPageSourcePieceOOR(t *testing.T) {
	t.Parallel()
	s := &BytesPageSource{Data: make([]byte, 64*1024), PieceSize: 16 * 1024}
	if got := s.NumPieces(); got != 4 {
		t.Fatalf("setup: NumPieces = %d, want 4", got)
	}
	if _, err := s.Piece(4); err == nil {
		t.Error("Piece(4) on a 4-piece source should error")
	}
	if _, err := s.Piece(-1); err == nil {
		t.Error("Piece(-1) should error")
	}
}

// TestAtomicWriteRenameFailure — covers the Rename-error
// branch in atomicWrite. We plant a non-empty directory at
// the destination path so os.Rename(tmp, path) fails (can't
// replace a non-empty dir with a file). The function must
// surface the error AND clean up the tempfile (not leave a
// growing collection of *.tmp files next to the destination).
func TestAtomicWriteRenameFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	// Plant a non-empty dir at the destination.
	if err := os.MkdirAll(filepath.Join(target, "child"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := atomicWrite(target, []byte("data")); err == nil {
		t.Error("atomicWrite into non-empty-dir destination should error")
	}
	// Tempfile cleanup: the .tmp must NOT linger after the
	// failed rename.
	if _, err := os.Stat(target + ".tmp"); err == nil {
		t.Error(".tmp left behind after rename failure")
	}
}
