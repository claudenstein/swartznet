package engine

import (
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

// buildInfoFromFiles synthesises a metainfo.Info with the given piece length
// and file layout, populating the TorrentOffset field the same way
// anacrolix's upvert path does so buildFileMap sees realistic inputs.
func buildInfoFromFiles(pieceLen int64, files []metainfo.FileInfo) *metainfo.Info {
	var offset int64
	out := make([]metainfo.FileInfo, len(files))
	copy(out, files)
	for i := range out {
		out[i].TorrentOffset = offset
		offset += out[i].Length
	}
	total := offset
	// Pieces is a 20-byte-per-piece SHA-1 string in the bencoded form. The
	// buildFileMap path only uses PieceLength and UpvertedFiles, so a
	// placeholder Pieces matching NumPieces() keeps NumPieces() honest
	// without committing to real hash values.
	numPieces := int((total + pieceLen - 1) / pieceLen)
	return &metainfo.Info{
		Name:        "synthetic",
		PieceLength: pieceLen,
		Files:       out,
		Pieces:      make([]byte, numPieces*20),
	}
}

// TestBuildFileMapSingleFile covers the trivial single-file torrent case.
func TestBuildFileMapSingleFile(t *testing.T) {
	t.Parallel()
	info := &metainfo.Info{
		Name:        "single.iso",
		PieceLength: 1024,
		Length:      4096, // single-file torrent — Length set, Files empty
		Pieces:      make([]byte, 4*20),
	}
	fm, err := buildFileMap(info)
	if err != nil {
		t.Fatalf("buildFileMap: %v", err)
	}
	if got := fm.NumPieces(); got != 4 {
		t.Errorf("NumPieces = %d, want 4", got)
	}
	if got := len(fm.Files()); got != 1 {
		t.Errorf("Files len = %d, want 1", got)
	}
	span := fm.Files()[0]
	if span.BeginPiece != 0 || span.EndPiece != 4 {
		t.Errorf("span = [%d, %d), want [0, 4)", span.BeginPiece, span.EndPiece)
	}
	if span.Path != "single.iso" {
		t.Errorf("span.Path = %q, want %q", span.Path, "single.iso")
	}
	// Every piece should map back to file 0 and only file 0.
	for p := 0; p < 4; p++ {
		got := fm.FilesForPiece(p)
		if len(got) != 1 || got[0] != 0 {
			t.Errorf("FilesForPiece(%d) = %v, want [0]", p, got)
		}
	}
}

// TestBuildFileMapMultiFile covers the usual multi-file torrent where pieces
// at file boundaries overlap two files.
func TestBuildFileMapMultiFile(t *testing.T) {
	t.Parallel()
	// Piece length 1024; files sized so pieces 2 and 5 straddle two files:
	//   file A: 0..2047     → pieces 0..1 (exactly two whole pieces)
	//   file B: 2048..3500  → pieces 2..3 (piece 3 is a partial, includes tail of B)
	//   file C: 3501..6000  → pieces 3..5 (piece 3 straddles B and C)
	info := buildInfoFromFiles(1024, []metainfo.FileInfo{
		{Length: 2048, Path: []string{"a"}},
		{Length: 1453, Path: []string{"b"}},
		{Length: 2500, Path: []string{"c"}},
	})
	fm, err := buildFileMap(info)
	if err != nil {
		t.Fatalf("buildFileMap: %v", err)
	}
	// Total length 6001 bytes, piece length 1024 → ceil(6001/1024) = 6.
	if got := fm.NumPieces(); got != 6 {
		t.Errorf("NumPieces = %d, want 6", got)
	}
	files := fm.Files()
	if len(files) != 3 {
		t.Fatalf("Files len = %d, want 3", len(files))
	}

	// file A [0, 2048) → pieces [0, 2)
	if files[0].BeginPiece != 0 || files[0].EndPiece != 2 {
		t.Errorf("A piece range = [%d, %d), want [0, 2)", files[0].BeginPiece, files[0].EndPiece)
	}
	// file B offset 2048, length 1453, end 3501 → pieces [2, 4)
	if files[1].BeginPiece != 2 || files[1].EndPiece != 4 {
		t.Errorf("B piece range = [%d, %d), want [2, 4)", files[1].BeginPiece, files[1].EndPiece)
	}
	// file C offset 3501, length 2500, end 6001 → pieces [3, 6)
	if files[2].BeginPiece != 3 || files[2].EndPiece != 6 {
		t.Errorf("C piece range = [%d, %d), want [3, 6)", files[2].BeginPiece, files[2].EndPiece)
	}

	// Piece 3 must map to both B and C (and only those).
	got := fm.FilesForPiece(3)
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("FilesForPiece(3) = %v, want [1, 2]", got)
	}
	// Piece 0 must map only to A.
	got = fm.FilesForPiece(0)
	if len(got) != 1 || got[0] != 0 {
		t.Errorf("FilesForPiece(0) = %v, want [0]", got)
	}
	// Piece 5 must map only to C.
	got = fm.FilesForPiece(5)
	if len(got) != 1 || got[0] != 2 {
		t.Errorf("FilesForPiece(5) = %v, want [2]", got)
	}
}

// TestBuildFileMapZeroLengthFile verifies that empty files (legal per BEP)
// do not crash the mapping and do not claim any pieces.
func TestBuildFileMapZeroLengthFile(t *testing.T) {
	t.Parallel()
	info := buildInfoFromFiles(1024, []metainfo.FileInfo{
		{Length: 1024, Path: []string{"a"}},
		{Length: 0, Path: []string{"empty"}},
		{Length: 512, Path: []string{"b"}},
	})
	fm, err := buildFileMap(info)
	if err != nil {
		t.Fatalf("buildFileMap: %v", err)
	}
	files := fm.Files()
	if len(files) != 3 {
		t.Fatalf("Files len = %d, want 3", len(files))
	}
	// Zero-length file has Begin == End, so numPieces contribution is 0.
	if files[1].BeginPiece != files[1].EndPiece {
		t.Errorf("zero-length file span = [%d, %d), want degenerate", files[1].BeginPiece, files[1].EndPiece)
	}
	// Make sure no piece maps to the zero-length file.
	for p := 0; p < fm.NumPieces(); p++ {
		for _, fi := range fm.FilesForPiece(p) {
			if fi == 1 {
				t.Errorf("piece %d should not map to zero-length file", p)
			}
		}
	}
}

// TestBuildFileMapInvalid exercises the defensive error paths.
func TestBuildFileMapInvalid(t *testing.T) {
	t.Parallel()
	if _, err := buildFileMap(nil); err == nil {
		t.Error("nil info: want error")
	}
	bad := &metainfo.Info{PieceLength: 0, Length: 10}
	if _, err := buildFileMap(bad); err == nil {
		t.Error("zero piece length: want error")
	}
}
