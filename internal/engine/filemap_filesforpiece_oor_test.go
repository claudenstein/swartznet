package engine

import (
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

// TestFilesForPieceOutOfRange covers the two guard branches in
// FilesForPiece: piece < 0 and piece >= numPieces. The existing
// filemap_test.go covers the in-range case.
func TestFilesForPieceOutOfRange(t *testing.T) {
	t.Parallel()
	info := buildInfoFromFiles(1<<14, []metainfo.FileInfo{
		{Path: []string{"a.bin"}, Length: 16384},
		{Path: []string{"b.bin"}, Length: 16384},
	})
	fm, err := buildFileMap(info)
	if err != nil {
		t.Fatalf("buildFileMap: %v", err)
	}
	if got := fm.FilesForPiece(-1); got != nil {
		t.Errorf("FilesForPiece(-1) = %v, want nil", got)
	}
	if got := fm.FilesForPiece(fm.NumPieces()); got != nil {
		t.Errorf("FilesForPiece(numPieces) = %v, want nil", got)
	}
	if got := fm.FilesForPiece(fm.NumPieces() + 5); got != nil {
		t.Errorf("FilesForPiece(numPieces+5) = %v, want nil", got)
	}
}
