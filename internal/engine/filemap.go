package engine

import (
	"fmt"

	"github.com/anacrolix/torrent/metainfo"
)

// fileMap describes which pieces cover which files in a torrent.
//
// It is a pure, in-memory view computed from a *metainfo.Info; this makes
// the mapping logic unit-testable without spinning up a real torrent. The
// live fileTracker goroutine uses the same fileMap on the hot path.
type fileMap struct {
	numPieces int
	files     []fileSpan
	// pieceToFiles[piece] lists the file indices the piece overlaps.
	// Pre-computed so the tracker hot path is O(files-per-piece), which
	// is ~1 for large torrents and at most a handful at file boundaries.
	pieceToFiles [][]int
}

// fileSpan is the per-file range of piece indices covering that file plus
// the metadata the indexer and the file-complete events need.
type fileSpan struct {
	Index      int    // file index in the upverted file list
	Path       string // user-visible path
	Size       int64  // file length in bytes
	BeginPiece int    // inclusive
	EndPiece   int    // exclusive (half-open interval)
}

// NumPieces returns the count of pieces in the mapped torrent.
func (fm *fileMap) NumPieces() int { return fm.numPieces }

// Files returns the per-file spans in declaration order.
func (fm *fileMap) Files() []fileSpan { return fm.files }

// FilesForPiece returns the file indices whose byte range overlaps the given
// piece index. Returns nil for out-of-range indices.
func (fm *fileMap) FilesForPiece(piece int) []int {
	if piece < 0 || piece >= fm.numPieces {
		return nil
	}
	return fm.pieceToFiles[piece]
}

// buildFileMap constructs a fileMap from the given metainfo.Info. The caller
// is responsible for ensuring info is non-nil and has its piece length set
// (which is always true for live anacrolix torrents after GotInfo fires).
//
// This function is pure and has no side effects; it is the unit-testable
// core of the file-completion tracker.
func buildFileMap(info *metainfo.Info) (*fileMap, error) {
	if info == nil {
		return nil, fmt.Errorf("engine: buildFileMap: nil info")
	}
	if info.PieceLength <= 0 {
		return nil, fmt.Errorf("engine: buildFileMap: invalid PieceLength %d", info.PieceLength)
	}

	numPieces := info.NumPieces()
	upverted := info.UpvertedFiles()

	fm := &fileMap{
		numPieces:    numPieces,
		files:        make([]fileSpan, 0, len(upverted)),
		pieceToFiles: make([][]int, numPieces),
	}

	for i, fi := range upverted {
		span := fileSpan{
			Index:      i,
			Path:       fi.DisplayPath(info),
			Size:       fi.Length,
			BeginPiece: fi.BeginPieceIndex(info.PieceLength),
			EndPiece:   fi.EndPieceIndex(info.PieceLength),
		}
		// Clamp: a file with Length == 0 can legitimately yield
		// BeginPiece == EndPiece; tracker treats those as immediately done.
		if span.BeginPiece < 0 {
			span.BeginPiece = 0
		}
		if span.EndPiece > numPieces {
			span.EndPiece = numPieces
		}
		fm.files = append(fm.files, span)
		for p := span.BeginPiece; p < span.EndPiece; p++ {
			fm.pieceToFiles[p] = append(fm.pieceToFiles[p], i)
		}
	}

	return fm, nil
}
