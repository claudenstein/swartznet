package daemon

import (
	"github.com/swartznet/swartznet/internal/engine"
	"github.com/swartznet/swartznet/internal/httpapi"
)

// controllerAdapter translates engine types into httpapi's locally-declared
// DTOs field by field. This is what keeps httpapi's zero-subsystem-import
// law true: httpapi sees only its own types, satisfied here.
type controllerAdapter struct {
	eng *engine.Engine
}

func (a *controllerAdapter) AddMagnetURI(uri string) (string, error) {
	return a.eng.AddMagnetURI(uri)
}

func (a *controllerAdapter) TorrentSnapshots() []httpapi.TorrentSnapshot {
	snaps := a.eng.TorrentSnapshots()
	out := make([]httpapi.TorrentSnapshot, 0, len(snaps))
	for _, s := range snaps {
		out = append(out, httpapi.TorrentSnapshot{
			InfoHash:       s.InfoHash,
			Name:           s.Name,
			Size:           s.Size,
			BytesCompleted: s.BytesCompleted,
			BytesMissing:   s.BytesMissing,
			Progress:       s.Progress,
			Files:          s.Files,
			ActivePeers:    s.ActivePeers,
			HalfOpenPeers:  s.HalfOpenPeers,
			PendingPeers:   s.PendingPeers,
			TotalPeers:     s.TotalPeers,
			Seeders:        s.Seeders,
			Paused:         s.Paused,
			Status:         s.Status,
			Indexing:       s.Indexing,
			IndexedFiles:   s.IndexedFiles,
			IndexExtracted: s.IndexExtracted,
			Queued:         s.Queued,
			DownloadRate:   s.DownloadRate,
			UploadRate:     s.UploadRate,
			SignedBy:       s.SignedBy,
			TrustedPub:     s.TrustedPub,
		})
	}
	return out
}

func (a *controllerAdapter) TorrentFiles(ihHex string) ([]httpapi.TorrentFile, error) {
	files, err := a.eng.TorrentFiles(ihHex)
	if err != nil {
		return nil, err
	}
	out := make([]httpapi.TorrentFile, 0, len(files))
	for _, f := range files {
		out = append(out, httpapi.TorrentFile{
			Index:          f.Index,
			Path:           f.Path,
			DisplayPath:    f.DisplayPath,
			Length:         f.Length,
			BytesCompleted: f.BytesCompleted,
			Progress:       f.Progress,
			Priority:       f.Priority,
		})
	}
	return out, nil
}

func (a *controllerAdapter) SetFilePriority(ihHex string, idx int, priority string) error {
	return a.eng.SetFilePriority(ihHex, idx, priority)
}

func (a *controllerAdapter) PauseTorrent(ihHex string) error  { return a.eng.PauseTorrent(ihHex) }
func (a *controllerAdapter) ResumeTorrent(ihHex string) error { return a.eng.ResumeTorrent(ihHex) }
func (a *controllerAdapter) RemoveTorrent(ihHex string, forget bool) error {
	if err := a.eng.RemoveTorrent(ihHex); err != nil {
		return err
	}
	if forget {
		// Deleting index docs after the engine drop keeps deletion off the
		// hot engine path; a nil index makes ForgetIndex a no-op, so Forget
		// still succeeds when Layer L is disabled.
		a.eng.ForgetIndex(ihHex)
	}
	return nil
}

func (a *controllerAdapter) SetTorrentIndexing(ihHex string, enabled bool) error {
	return a.eng.SetTorrentIndexing(ihHex, enabled)
}

func (a *controllerAdapter) UploadLimitBytesPerSec() int64   { return a.eng.UploadLimitBytesPerSec() }
func (a *controllerAdapter) DownloadLimitBytesPerSec() int64 { return a.eng.DownloadLimitBytesPerSec() }
func (a *controllerAdapter) SetUploadLimitBytesPerSec(bps int64) {
	a.eng.SetUploadLimitBytesPerSec(bps)
}
func (a *controllerAdapter) SetDownloadLimitBytesPerSec(bps int64) {
	a.eng.SetDownloadLimitBytesPerSec(bps)
}

func (a *controllerAdapter) MaxActiveDownloads() int     { return a.eng.MaxActiveDownloads() }
func (a *controllerAdapter) SetMaxActiveDownloads(n int) { a.eng.SetMaxActiveDownloads(n) }
