package dhtindex

import (
	"bytes"
	"fmt"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/swartznet/swartznet/contracts/snagg"
)

// SnaggPieceLength is the torrent piece length for a wrapped SNAGG tree. It
// MUST equal snagg.MinPieceSize: a SNAGG file is queried through
// snagg.BytesPageSource with PieceSize == the tree's page size, so the wrapping
// torrent's pieces have to align 1:1 with the tree's pages. Wrapping a tree
// built at a different page size would desync the reader.
const SnaggPieceLength = snagg.MinPieceSize

// WrapSnaggTorrent wraps a signed SNAGG file as a single-file, trackerless v1
// metainfo whose piece length aligns with the tree's pages. The returned
// metainfo is what a publisher seeds and advertises (its infohash) in a
// PPMIValue; a subscriber fetches it and reads the file back through a
// BytesPageSource of the same piece size.
func WrapSnaggTorrent(name string, snaggBytes []byte) (*metainfo.MetaInfo, error) {
	if len(snaggBytes) == 0 {
		return nil, fmt.Errorf("dhtindex: empty SNAGG payload")
	}
	if len(snaggBytes)%SnaggPieceLength != 0 {
		return nil, fmt.Errorf("dhtindex: SNAGG payload %d bytes not a multiple of piece length %d", len(snaggBytes), SnaggPieceLength)
	}
	pieces, err := metainfo.GeneratePieces(bytes.NewReader(snaggBytes), SnaggPieceLength, nil)
	if err != nil {
		return nil, fmt.Errorf("dhtindex: generate pieces: %w", err)
	}
	info := metainfo.Info{
		Name:        name,
		Length:      int64(len(snaggBytes)),
		PieceLength: SnaggPieceLength,
		Pieces:      pieces,
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		return nil, fmt.Errorf("dhtindex: marshal info: %w", err)
	}
	return &metainfo.MetaInfo{InfoBytes: infoBytes}, nil
}

// OpenVerifiedTree opens a fetched SNAGG file and, when expectedCommit is a
// 32-byte PPMI commit, requires the tree's fingerprint to match it — so a
// subscriber never trusts a tree whose bytes disagree with the signed pointer
// that led it there. OpenBTree already verifies the trailer signature before
// returning; the commit check binds the pointer to the tree. An empty
// expectedCommit skips the binding (the trailer signature still gates trust).
func OpenVerifiedTree(data, expectedCommit []byte) (*snagg.Tree, error) {
	tree, err := snagg.OpenBTree(snagg.BytesPageSource{Data: data, PieceSize: SnaggPieceLength})
	if err != nil {
		return nil, err
	}
	if len(expectedCommit) == 32 {
		if !bytes.Equal(tree.Trailer.Fingerprint[:], expectedCommit) {
			return nil, fmt.Errorf("dhtindex: fetched tree fingerprint does not match the PPMI commit")
		}
	}
	return tree, nil
}
