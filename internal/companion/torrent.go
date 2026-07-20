package companion

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// CompanionPieceLength is the fixed piece size for companion torrents (256 KiB)
// — small payloads, so a fixed length keeps the metainfo deterministic.
const CompanionPieceLength = 256 * 1024

// WriteCompanionFiles encodes idx to gzip(JSON), writes it atomically under dir
// as CompanionFileName(idx.Publisher), builds a single-file trackerless v1
// metainfo over the payload, writes that atomically as companion.torrent, and
// returns (jsonPath, metainfo). Companion torrents carry NO announce list —
// they are discovered via the BEP-46 pointer, not trackers.
func WriteCompanionFiles(dir string, idx CompanionIndex) (string, *metainfo.MetaInfo, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, fmt.Errorf("companion: mkdir %q: %w", dir, err)
	}
	payload, err := Encode(idx)
	if err != nil {
		return "", nil, err
	}
	jsonPath := filepath.Join(dir, CompanionFileName(idx.Publisher))
	if err := atomicWrite(jsonPath, payload); err != nil {
		return "", nil, err
	}
	mi, err := buildMetaInfoForFile(filepath.Base(jsonPath), payload)
	if err != nil {
		return "", nil, err
	}
	miBytes, err := bencode.Marshal(*mi)
	if err != nil {
		return "", nil, fmt.Errorf("companion: marshal metainfo: %w", err)
	}
	if err := atomicWrite(filepath.Join(dir, "companion.torrent"), miBytes); err != nil {
		return "", nil, err
	}
	return jsonPath, mi, nil
}

// buildMetaInfoForFile builds a single-file v1 metainfo over payload with the
// given name. Pieces are generated from the in-memory bytes (no second disk
// read), matching the on-disk file exactly.
func buildMetaInfoForFile(name string, payload []byte) (*metainfo.MetaInfo, error) {
	pieces, err := metainfo.GeneratePieces(bytes.NewReader(payload), CompanionPieceLength, nil)
	if err != nil {
		return nil, fmt.Errorf("companion: generate pieces: %w", err)
	}
	info := metainfo.Info{
		Name:        name,
		Length:      int64(len(payload)),
		PieceLength: CompanionPieceLength,
		Pieces:      pieces,
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		return nil, fmt.Errorf("companion: marshal info: %w", err)
	}
	return &metainfo.MetaInfo{InfoBytes: infoBytes}, nil
}

// atomicWrite writes data to path via a tempfile + rename, mode 0600.
func atomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("companion: write tmp %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("companion: rename %q: %w", path, err)
	}
	return nil
}
