package engine

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/swartznet/swartznet/internal/identity"
	"github.com/swartznet/swartznet/internal/signing"
)

// CreateTorrentOptions parameterizes torrent creation. These are standalone
// functions, not Engine methods: a plain hash-only create must not bind
// sockets or create XDG state.
type CreateTorrentOptions struct {
	// Root is the file or directory to hash (required).
	Root string
	// Name overrides info.name (default: basename of Root).
	Name string
	// PieceLength in bytes; 0 = auto (anacrolix targets 1024–2048 pieces).
	// Non-zero values must be a power of two ≥ 16 KiB.
	PieceLength int64
	// Trackers: first becomes announce, all become one announce-list tier.
	// Empty = trackerless (no announce keys emitted).
	Trackers []string
	// WebSeeds populate url-list (BEP-19).
	WebSeeds []string
	// Private sets info.private (BEP-27) — inside the info dict, so it
	// changes the infohash.
	Private bool
	// Comment is the optional top-level comment.
	Comment string
	// CreatedBy defaults to "SwartzNet".
	CreatedBy string
	// SignWith, when non-nil, signs the written .torrent (top-level snet.*
	// fields; the infohash is unaffected).
	SignWith *identity.Signer
}

// CreateTorrent builds the metainfo. The infohash depends only on the info
// dict (content + name + piece length + private); creation date, comment,
// created-by, trackers, webseeds, and snet.* are all top-level.
func CreateTorrent(opts CreateTorrentOptions) (*metainfo.MetaInfo, error) {
	if opts.Root == "" {
		return nil, fmt.Errorf("engine: CreateTorrent requires opts.Root")
	}
	st, err := os.Stat(opts.Root)
	if err != nil {
		return nil, fmt.Errorf("stat root: %w", err)
	}
	var totalSize int64
	fileCount := 0
	if st.IsDir() {
		err := filepath.WalkDir(opts.Root, func(_ string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				info, err := d.Info()
				if err != nil {
					return err
				}
				totalSize += info.Size()
				fileCount++
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("stat tree: %w", err)
		}
		if fileCount == 0 {
			// Pinned explicitly: an empty directory would otherwise produce a
			// degenerate zero-file torrent (unverified legacy behavior).
			return nil, fmt.Errorf("engine: root directory contains no files")
		}
	} else {
		totalSize = st.Size()
	}

	pieceLen := opts.PieceLength
	if pieceLen == 0 {
		pieceLen = metainfo.ChoosePieceLength(totalSize)
	} else if pieceLen < 16*1024 || pieceLen&(pieceLen-1) != 0 {
		// The legacy documented this constraint but never enforced it,
		// silently producing broken torrents. Fail closed instead.
		return nil, fmt.Errorf("engine: piece length must be a power of two ≥ 16 KiB (got %d)", pieceLen)
	}

	info := metainfo.Info{PieceLength: pieceLen}
	if opts.Private {
		private := true
		info.Private = &private
	}
	if err := info.BuildFromFilePath(opts.Root); err != nil {
		return nil, fmt.Errorf("build info: %w", err)
	}
	// The override applies AFTER BuildFromFilePath (which sets basename).
	if opts.Name != "" {
		info.Name = opts.Name
	}

	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		return nil, fmt.Errorf("marshal info: %w", err)
	}
	mi := &metainfo.MetaInfo{
		InfoBytes:    infoBytes,
		CreationDate: time.Now().Unix(),
		Comment:      opts.Comment,
	}
	mi.CreatedBy = opts.CreatedBy
	if mi.CreatedBy == "" {
		mi.CreatedBy = "SwartzNet"
	}
	if len(opts.Trackers) > 0 {
		mi.Announce = opts.Trackers[0]
		mi.AnnounceList = [][]string{opts.Trackers}
	}
	if len(opts.WebSeeds) > 0 {
		mi.UrlList = opts.WebSeeds
	}
	return mi, nil
}

// CreateTorrentFile creates and atomically writes a .torrent. Signing (when
// requested) happens on the marshaled bytes AFTER hashing and BEFORE the
// write, so the on-disk file carries the signature while the infohash is
// untouched. Returns the 40-hex infohash and the FINAL written bytes — the
// signed bytes are what a --seed run must feed the engine, so the creator's
// own node sees its signature.
func CreateTorrentFile(opts CreateTorrentOptions, outPath string) (string, []byte, error) {
	mi, err := CreateTorrent(opts)
	if err != nil {
		return "", nil, err
	}
	var buf []byte
	buf, err = bencode.Marshal(*mi)
	if err != nil {
		return "", nil, fmt.Errorf("marshal metainfo: %w", err)
	}
	if opts.SignWith != nil {
		buf, err = signing.Sign(buf, *opts.SignWith)
		if err != nil {
			return "", nil, fmt.Errorf("sign: %w", err)
		}
	}
	tmp := outPath + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o644); err != nil {
		return "", nil, fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, outPath); err != nil {
		_ = os.Remove(tmp)
		return "", nil, fmt.Errorf("rename: %w", err)
	}
	return mi.HashInfoBytes().HexString(), buf, nil
}
