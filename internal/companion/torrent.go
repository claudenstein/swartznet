package companion

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// CompanionPieceLength is the piece length used for companion
// torrents. 256 KiB is large enough to keep the piece count
// manageable for ~100 MB JSON.gz blobs and small enough that
// individual peers can verify pieces quickly.
const CompanionPieceLength int64 = 256 * 1024

// WriteCompanionFiles serialises a CompanionIndex to disk and
// produces the matching v1 .torrent metainfo. The two files are
// written to dir as:
//
//	<dir>/<FormatFileName>     # the gzipped JSON payload
//	<dir>/companion.torrent    # the metainfo wrapping it
//
// The returned MetaInfo is also constructed in memory so the
// caller can hand it directly to torrent.Client.AddTorrent
// without re-reading from disk.
//
// The publisher MUST treat dir as exclusive — re-running the
// publisher overwrites both files in place. The infohash
// usually changes between runs because the JSON includes a
// fresh GeneratedAt timestamp on every Encode call.
func WriteCompanionFiles(dir string, idx CompanionIndex) (string, *metainfo.MetaInfo, error) {
	if dir == "" {
		return "", nil, errors.New("companion: empty dir")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, fmt.Errorf("companion: mkdir %q: %w", dir, err)
	}

	payload, err := Encode(idx)
	if err != nil {
		return "", nil, err
	}

	jsonPath := filepath.Join(dir, FormatFileName)
	if err := atomicWrite(jsonPath, payload); err != nil {
		return "", nil, fmt.Errorf("companion: write payload: %w", err)
	}

	mi, err := buildMetaInfoForFile(jsonPath, payload)
	if err != nil {
		return "", nil, err
	}

	miBytes, err := bencode.Marshal(mi)
	if err != nil {
		return "", nil, fmt.Errorf("companion: marshal metainfo: %w", err)
	}
	torrentPath := filepath.Join(dir, "companion.torrent")
	if err := atomicWrite(torrentPath, miBytes); err != nil {
		return "", nil, fmt.Errorf("companion: write torrent: %w", err)
	}

	return jsonPath, mi, nil
}

// buildMetaInfoForFile constructs a v1 .torrent MetaInfo for a
// single file whose name and contents are known. We avoid
// metainfo.Info.BuildFromFilePath because that path opens the
// file again — we already have the bytes in memory and can
// generate pieces directly without a second disk read.
func buildMetaInfoForFile(filePath string, payload []byte) (*metainfo.MetaInfo, error) {
	info := metainfo.Info{
		Name:        filepath.Base(filePath),
		Length:      int64(len(payload)),
		PieceLength: CompanionPieceLength,
	}
	pieces, err := metainfo.GeneratePieces(bytes.NewReader(payload), info.PieceLength, nil)
	if err != nil {
		return nil, fmt.Errorf("companion: generate pieces: %w", err)
	}
	info.Pieces = pieces

	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		return nil, fmt.Errorf("companion: marshal info: %w", err)
	}

	mi := &metainfo.MetaInfo{
		InfoBytes: infoBytes,
		// Empty AnnounceList: companion torrents are
		// discovered via the M11c BEP-46 pointer, not via
		// trackers. The pointer is enough.
	}
	return mi, nil
}

// atomicWrite writes data to path atomically via tempfile +
// rename. Used by WriteCompanionFiles so a partial write never
// corrupts the publisher's state.
func atomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, bytes.NewReader(data)); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
