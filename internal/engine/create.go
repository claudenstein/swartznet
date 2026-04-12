package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// CreateTorrentOptions describes the torrent to build.
type CreateTorrentOptions struct {
	// Root is the file or directory to hash. Required. A directory
	// produces a multi-file torrent; a regular file produces a
	// single-file torrent.
	Root string

	// Name overrides the info.name field. When empty, the basename
	// of Root is used.
	Name string

	// PieceLength in bytes. Zero means "pick automatically via
	// metainfo.ChoosePieceLength" which targets 1024–2048 pieces
	// total. Must be a power of two ≥ 16 KiB when non-zero.
	PieceLength int64

	// Trackers is a list of announce URLs. The first URL becomes
	// the primary (mi.Announce); all URLs go into AnnounceList
	// as a single tier. Empty is valid (trackerless torrents
	// discoverable via DHT only).
	Trackers []string

	// WebSeeds are HTTP(S) URLs that serve the exact content
	// layout (BEP-19). Optional.
	WebSeeds []string

	// Private, when true, marks the torrent as private (BEP-27):
	// DHT and PEX discovery are disabled; peers come only from
	// the listed trackers.
	Private bool

	// Comment is an arbitrary human-readable note baked into the
	// .torrent file.
	Comment string

	// CreatedBy identifies the tool that built the torrent. When
	// empty, defaults to "SwartzNet".
	CreatedBy string
}

// CreateTorrent hashes the content at opts.Root and builds an
// in-memory *metainfo.MetaInfo. The caller can then either write
// the returned bytes to disk as a .torrent file or pass the
// MetaInfo to Engine.AddTorrentMetaInfo to start seeding
// immediately.
//
// Piece hashing is synchronous and I/O-bound — expect minutes for
// large directories. Run from a goroutine if calling from a UI
// thread. This method does not write any file; it only builds
// the in-memory structure. See CreateTorrentFile for the
// write-to-disk convenience wrapper.
func (e *Engine) CreateTorrent(opts CreateTorrentOptions) (*metainfo.MetaInfo, error) {
	if opts.Root == "" {
		return nil, errors.New("engine: CreateTorrent requires opts.Root")
	}
	st, err := os.Stat(opts.Root)
	if err != nil {
		return nil, fmt.Errorf("stat root: %w", err)
	}
	// Pre-compute the total size so we can ask ChoosePieceLength
	// for a sensible default when the caller didn't specify one.
	var totalSize int64
	if st.IsDir() {
		// BuildFromFilePath walks the tree itself and hashes every
		// file; this extra walk just sums sizes so we can pick a
		// sensible default piece length. Cheap relative to hashing.
		err = filepath.WalkDir(opts.Root, func(_ string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			totalSize += info.Size()
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("stat tree: %w", err)
		}
	} else {
		totalSize = st.Size()
	}

	pieceLen := opts.PieceLength
	if pieceLen == 0 {
		pieceLen = metainfo.ChoosePieceLength(totalSize)
	}

	info := metainfo.Info{
		PieceLength: pieceLen,
	}
	if opts.Name != "" {
		info.Name = opts.Name
	}
	if opts.Private {
		info.Private = &opts.Private
	}

	if err := info.BuildFromFilePath(opts.Root); err != nil {
		return nil, fmt.Errorf("build info: %w", err)
	}

	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		return nil, fmt.Errorf("marshal info: %w", err)
	}

	mi := &metainfo.MetaInfo{
		InfoBytes:    infoBytes,
		Comment:      opts.Comment,
		UrlList:      opts.WebSeeds,
		CreationDate: time.Now().Unix(),
	}
	if opts.CreatedBy != "" {
		mi.CreatedBy = opts.CreatedBy
	} else {
		mi.CreatedBy = "SwartzNet"
	}
	if len(opts.Trackers) > 0 {
		mi.Announce = opts.Trackers[0]
		mi.AnnounceList = [][]string{opts.Trackers}
	}

	return mi, nil
}

// CreateTorrentFile is CreateTorrent + Write to a .torrent file at
// outPath. Overwrites an existing file atomically (temp + rename).
// Returns the infohash (40-char hex) and the MetaInfo.
func (e *Engine) CreateTorrentFile(opts CreateTorrentOptions, outPath string) (string, *metainfo.MetaInfo, error) {
	mi, err := e.CreateTorrent(opts)
	if err != nil {
		return "", nil, err
	}

	tmp := outPath + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return "", nil, fmt.Errorf("open tmp: %w", err)
	}
	if err := mi.Write(f); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return "", nil, fmt.Errorf("write torrent: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", nil, fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmp, outPath); err != nil {
		_ = os.Remove(tmp)
		return "", nil, fmt.Errorf("rename: %w", err)
	}

	ih := mi.HashInfoBytes()
	return ih.HexString(), mi, nil
}
