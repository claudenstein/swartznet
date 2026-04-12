package engine

import (
	"errors"
	"fmt"

	"github.com/anacrolix/torrent"
)

// FileSnapshot is the per-file view returned by Engine.TorrentFiles.
// Populated at call time from the underlying anacrolix/torrent File.
type FileSnapshot struct {
	// Index is the file's position in t.Files().
	Index int
	// Path is the path relative to the torrent's info.name
	// directory. For single-file torrents this equals Name.
	Path string
	// DisplayPath is the user-visible rendering including the
	// torrent name prefix.
	DisplayPath string
	// Length is the total bytes of this file.
	Length int64
	// BytesCompleted is the bytes verified on disk.
	BytesCompleted int64
	// Progress is BytesCompleted / Length in [0, 1].
	Progress float64
	// Priority is the current piece priority applied to this
	// file. "none" means the file will not be downloaded.
	Priority string
}

// FilePriority is the stringified form of torrent.PiecePriority the
// GUI exposes. We keep it as a small enum (none / normal / high) to
// match common torrent-client conventions; internally this maps to
// anacrolix's PiecePriorityNone / PiecePriorityNormal /
// PiecePriorityHigh.
type FilePriority string

const (
	FilePriorityNone   FilePriority = "none"
	FilePriorityNormal FilePriority = "normal"
	FilePriorityHigh   FilePriority = "high"
)

func (p FilePriority) toAnacrolix() (torrent.PiecePriority, error) {
	switch p {
	case FilePriorityNone:
		return torrent.PiecePriorityNone, nil
	case FilePriorityNormal, "":
		return torrent.PiecePriorityNormal, nil
	case FilePriorityHigh:
		return torrent.PiecePriorityHigh, nil
	}
	return 0, fmt.Errorf("engine: unknown file priority %q (want none/normal/high)", p)
}

func priorityLabel(p torrent.PiecePriority) string {
	switch p {
	case torrent.PiecePriorityNone:
		return "none"
	case torrent.PiecePriorityHigh:
		return "high"
	default:
		// PiecePriorityNormal + the internal PiecePriorityReadahead /
		// PiecePriorityNext / PiecePriorityNow all surface as "normal"
		// for UI purposes; the distinctions matter only inside the
		// request strategy.
		return "normal"
	}
}

// TorrentFiles returns a per-file snapshot for the torrent. Returns
// an error if the infohash is unknown. If the torrent has not yet
// received its info dictionary, returns an empty slice.
func (e *Engine) TorrentFiles(infoHashHex string) ([]FileSnapshot, error) {
	h, err := e.handleByHex(infoHashHex)
	if err != nil {
		return nil, err
	}
	if h.T.Info() == nil {
		return []FileSnapshot{}, nil
	}
	files := h.T.Files()
	out := make([]FileSnapshot, 0, len(files))
	for i, f := range files {
		length := f.Length()
		completed := f.BytesCompleted()
		var progress float64
		if length > 0 {
			progress = float64(completed) / float64(length)
			if progress > 1 {
				progress = 1
			}
		}
		out = append(out, FileSnapshot{
			Index:          i,
			Path:           f.Path(),
			DisplayPath:    f.DisplayPath(),
			Length:         length,
			BytesCompleted: completed,
			Progress:       progress,
			Priority:       priorityLabel(f.Priority()),
		})
	}
	return out, nil
}

// SetFilePriority flips the download priority for a single file in
// a multi-file torrent. Priority "none" removes the file from the
// download set (anacrolix will skip its pieces and not verify them
// against peers); "normal" and "high" both include the file but
// "high" asks anacrolix to request those pieces first. Idempotent.
func (e *Engine) SetFilePriority(infoHashHex string, fileIndex int, priority FilePriority) error {
	h, err := e.handleByHex(infoHashHex)
	if err != nil {
		return err
	}
	if h.T.Info() == nil {
		return errors.New("engine: torrent metadata not yet available")
	}
	files := h.T.Files()
	if fileIndex < 0 || fileIndex >= len(files) {
		return fmt.Errorf("engine: file index %d out of range [0, %d)", fileIndex, len(files))
	}
	prio, err := priority.toAnacrolix()
	if err != nil {
		return err
	}
	files[fileIndex].SetPriority(prio)
	e.log.Info("engine.file_priority_set",
		"info_hash", infoHashHex,
		"file_index", fileIndex,
		"priority", string(priority),
	)
	return nil
}
