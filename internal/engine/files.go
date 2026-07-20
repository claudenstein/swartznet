package engine

import (
	"fmt"

	"github.com/anacrolix/torrent"
)

// FileSnapshot is one file's state for the files UI.
type FileSnapshot struct {
	Index          int
	Path           string
	DisplayPath    string
	Length         int64
	BytesCompleted int64
	Progress       float64
	Priority       string // none | normal | high
}

// parsePriority maps the user-facing priority names. Empty means normal.
func parsePriority(s string) (torrent.PiecePriority, error) {
	switch s {
	case "none":
		return torrent.PiecePriorityNone, nil
	case "normal", "":
		return torrent.PiecePriorityNormal, nil
	case "high":
		return torrent.PiecePriorityHigh, nil
	default:
		return 0, fmt.Errorf("engine: unknown file priority %q (want none/normal/high)", s)
	}
}

// priorityLabel collapses anacrolix's internal priorities for display: None
// and High keep their names; Normal and the internal Readahead/Next/Now all
// render "normal".
func priorityLabel(p torrent.PiecePriority) string {
	switch p {
	case torrent.PiecePriorityNone:
		return "none"
	case torrent.PiecePriorityHigh:
		return "high"
	default:
		return "normal"
	}
}

// TorrentFiles lists a torrent's files. Pre-metadata torrents yield an empty
// slice — not nil, not an error.
func (e *Engine) TorrentFiles(ihHex string) ([]FileSnapshot, error) {
	h, err := e.handleByHex(ihHex)
	if err != nil {
		return nil, err
	}
	out := make([]FileSnapshot, 0)
	if h.T.Info() == nil {
		return out, nil
	}
	for i, f := range h.T.Files() {
		var progress float64
		if f.Length() > 0 {
			progress = float64(f.BytesCompleted()) / float64(f.Length())
			if progress > 1 {
				progress = 1
			}
		}
		out = append(out, FileSnapshot{
			Index:          i,
			Path:           f.Path(),
			DisplayPath:    f.DisplayPath(),
			Length:         f.Length(),
			BytesCompleted: f.BytesCompleted(),
			Progress:       progress,
			Priority:       priorityLabel(f.Priority()),
		})
	}
	return out, nil
}

// SetFilePriority sets one file's priority. "none" removes the file from the
// download set; an all-none torrent stops occupying a queue slot, so a
// promotion pass runs after every change.
func (e *Engine) SetFilePriority(ihHex string, index int, priority string) error {
	h, err := e.handleByHex(ihHex)
	if err != nil {
		return err
	}
	if h.T.Info() == nil {
		return fmt.Errorf("engine: torrent metadata not yet available")
	}
	files := h.T.Files()
	if index < 0 || index >= len(files) {
		return fmt.Errorf("engine: file index %d out of range [0, %d)", index, len(files))
	}
	p, err := parsePriority(priority)
	if err != nil {
		return err
	}
	files[index].SetPriority(p)
	e.log.Info("engine.file_priority_set", "info_hash", h.InfoHashHex(), "file_index", index, "priority", priority)
	go e.promoteQueued()
	return nil
}
