package main

import (
	"github.com/swartznet/swartznet/internal/engine"
	"github.com/swartznet/swartznet/internal/httpapi"
)

// controllerAdapter satisfies httpapi.TorrentController by
// delegating to the engine. The engine returns its own
// engine.TorrentSnapshot type; we translate it into the
// httpapi.TorrentSnapshot shape one field at a time so the two
// packages can stay independent (httpapi must not import
// internal/engine).
type controllerAdapter struct {
	eng *engine.Engine
}

func (c *controllerAdapter) AddMagnetURI(uri string) (string, error) {
	return c.eng.AddMagnetURI(uri)
}

func (c *controllerAdapter) PauseTorrent(infoHashHex string) error {
	return c.eng.PauseTorrent(infoHashHex)
}

func (c *controllerAdapter) ResumeTorrent(infoHashHex string) error {
	return c.eng.ResumeTorrent(infoHashHex)
}

func (c *controllerAdapter) RemoveTorrent(infoHashHex string) error {
	return c.eng.RemoveTorrent(infoHashHex)
}

func (c *controllerAdapter) TorrentSnapshots() []httpapi.TorrentSnapshot {
	src := c.eng.TorrentSnapshots()
	out := make([]httpapi.TorrentSnapshot, 0, len(src))
	for _, s := range src {
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
		})
	}
	return out
}
