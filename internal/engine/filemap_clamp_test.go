package engine

import (
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

// TestBuildFileMapEndPieceClamp covers buildFileMap's
// `if span.EndPiece > numPieces { span.EndPiece = numPieces }`
// arm at filemap.go:84-86. Synthesise an Info whose Pieces array
// claims fewer pieces than the file's length actually needs —
// EndPieceIndex would otherwise return past the array bound, so
// the clamp is the line of defense.
func TestBuildFileMapEndPieceClamp(t *testing.T) {
	t.Parallel()
	info := &metainfo.Info{
		Name:        "lying",
		PieceLength: 1024,
		// Claim 1 piece in Pieces, but the Files slice describes a
		// 4096-byte file (which would span 4 pieces). NumPieces() =
		// len(Pieces)/20 = 1, so the clamp fires.
		Pieces: make([]byte, 1*20),
		Files: []metainfo.FileInfo{
			{Length: 4096, Path: []string{"big"}},
		},
	}
	fm, err := buildFileMap(info)
	if err != nil {
		t.Fatalf("buildFileMap: %v", err)
	}
	if got := fm.NumPieces(); got != 1 {
		t.Fatalf("NumPieces = %d, want 1", got)
	}
	files := fm.Files()
	if len(files) != 1 {
		t.Fatalf("Files len = %d, want 1", len(files))
	}
	if files[0].EndPiece != 1 {
		t.Errorf("EndPiece = %d, want 1 (clamped)", files[0].EndPiece)
	}
	// Piece 0 must still map to file 0 — the clamp must not
	// destroy the per-piece overlap list.
	if got := fm.FilesForPiece(0); len(got) != 1 || got[0] != 0 {
		t.Errorf("FilesForPiece(0) = %v, want [0]", got)
	}
}
