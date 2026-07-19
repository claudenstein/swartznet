package engine

import (
	"sort"
	"time"

	"golang.org/x/time/rate"
)

// TorrentSnapshot is a point-in-time view of one torrent for status
// surfaces. The daemon adapts it field-by-field into httpapi's DTOs.
type TorrentSnapshot struct {
	InfoHash       string
	Name           string
	Size           int64
	BytesCompleted int64
	BytesMissing   int64
	Progress       float64 // [0,1]
	Files          int
	ActivePeers    int
	HalfOpenPeers  int
	PendingPeers   int
	TotalPeers     int
	Seeders        int
	Paused         bool
	Status         string // metadata | downloading | seeding | paused | queued
	Indexing       bool
	IndexedFiles   int // Slice 4
	IndexExtracted int // Slice 4
	Queued         bool
	DownloadRate   int64
	UploadRate     int64
	SignedBy       string // Slice 3
	TrustedPub     bool   // Slice 5
}

// TorrentSnapshots snapshots every torrent — cheap enough for a polling
// HTTP handler.
func (e *Engine) TorrentSnapshots() []TorrentSnapshot {
	handles := e.Torrents()
	out := make([]TorrentSnapshot, 0, len(handles))
	store := e.TrustStore()
	for _, h := range handles {
		s := h.snapshot()
		s.IndexedFiles, s.IndexExtracted = e.IndexStats(s.InfoHash)
		s.TrustedPub = s.SignedBy != "" && store != nil && store.IsTrusted(s.SignedBy)
		out = append(out, s)
	}
	// Deterministic order by infohash. Torrents() ranges a map (randomized
	// iteration order), so without this the list reshuffles on every poll — a GUI
	// that tracks selection by row index would then act on the wrong torrent, and
	// any consumer diffing successive snapshots sees spurious churn.
	sort.Slice(out, func(i, j int) bool { return out[i].InfoHash < out[j].InfoHash })
	return out
}

// snapshot builds the view. Pre-metadata, only InfoHash/Name/Paused/Status/
// Indexing/Queued/SignedBy are populated — anacrolix v1.61.0 nil-panics in
// BytesMissing()/Stats()/Files()/Length() before Info() is non-nil.
func (h *Handle) snapshot() TorrentSnapshot {
	s := TorrentSnapshot{
		InfoHash: h.InfoHashHex(),
		Name:     h.T.Name(),
		Paused:   h.isPaused(),
		Indexing: h.isIndexing(),
		Queued:   h.isQueued(),
		SignedBy: h.SignedBy(),
	}
	info := h.T.Info()
	if info == nil {
		switch {
		case s.Paused:
			s.Status = "paused"
		case s.Queued:
			s.Status = "queued"
		default:
			s.Status = "metadata"
		}
		return s
	}

	s.Size = h.T.Length()
	s.BytesCompleted = h.T.BytesCompleted()
	s.BytesMissing = h.T.BytesMissing()
	if s.Size > 0 {
		s.Progress = float64(s.BytesCompleted) / float64(s.Size)
		if s.Progress > 1 {
			s.Progress = 1
		}
	}
	s.Files = len(h.T.Files())
	stats := h.T.Stats()
	s.ActivePeers = stats.ActivePeers
	s.HalfOpenPeers = stats.HalfOpenPeers
	s.PendingPeers = stats.PendingPeers
	s.TotalPeers = stats.TotalPeers
	s.Seeders = stats.ConnectedSeeders
	s.DownloadRate, s.UploadRate = h.sampleRate(
		stats.BytesReadUsefulData.Int64(), stats.BytesWrittenData.Int64())

	// Status precedence (exactly five values; "complete" is never emitted).
	switch {
	case s.Paused:
		s.Status = "paused"
	case s.BytesMissing == 0 && s.Size > 0:
		s.Status = "seeding"
	case s.Queued:
		s.Status = "queued"
	default:
		s.Status = "downloading"
	}
	return s
}

// sampleRate derives byte rates from ConnStats deltas: down from
// BytesReadUsefulData, up from BytesWrittenData (the counters are
// deliberately asymmetric). First call seeds state and reports 0; calls
// under 100 ms apart return the cached rates (built for 2 s UI polls).
func (h *Handle) sampleRate(read, written int64) (down, up int64) {
	h.rateMu.Lock()
	defer h.rateMu.Unlock()
	now := time.Now()
	if !h.rateSeeded {
		h.rateSeeded = true
		h.rateAt = now
		h.rateRead = read
		h.rateWrite = written
		return 0, 0
	}
	elapsed := now.Sub(h.rateAt)
	if elapsed < 100*time.Millisecond {
		return h.rateDown, h.rateUp
	}
	down = int64(float64(read-h.rateRead) / elapsed.Seconds())
	up = int64(float64(written-h.rateWrite) / elapsed.Seconds())
	if down < 0 {
		down = 0
	}
	if up < 0 {
		up = 0
	}
	h.rateAt = now
	h.rateRead = read
	h.rateWrite = written
	h.rateDown = down
	h.rateUp = up
	return down, up
}

// Rate limits: two shared limiters mutated in place so live connections see
// changes immediately. 0 (or negative) = unlimited.

// SetUploadLimitBytesPerSec sets the upload cap (≤0 = unlimited). Kept
// separate from the download setter so the HTTP layer can PATCH one side
// without touching the other.
func (e *Engine) SetUploadLimitBytesPerSec(bps int64) {
	setLimiterBytesPerSec(e.ulLimiter, bps)
	e.log.Info("engine.upload_limit_set", "bytes_per_sec", bps)
}

// SetDownloadLimitBytesPerSec sets the download cap (≤0 = unlimited).
func (e *Engine) SetDownloadLimitBytesPerSec(bps int64) {
	setLimiterBytesPerSec(e.dlLimiter, bps)
	e.log.Info("engine.download_limit_set", "bytes_per_sec", bps)
}

// UploadLimitBytesPerSec reports the upload cap, 0 for unlimited.
func (e *Engine) UploadLimitBytesPerSec() int64 { return limiterToBytesPerSec(e.ulLimiter) }

// DownloadLimitBytesPerSec reports the download cap, 0 for unlimited.
func (e *Engine) DownloadLimitBytesPerSec() int64 { return limiterToBytesPerSec(e.dlLimiter) }

func setLimiterBytesPerSec(l *rate.Limiter, bps int64) {
	if l == nil {
		return
	}
	if bps <= 0 {
		l.SetLimit(rate.Inf)
		l.SetBurst(unlimitedBurst)
		return
	}
	burst := int(bps)
	if burst < 16*1024 {
		burst = 16 * 1024 // floor: the largest single anacrolix reservation
	}
	l.SetLimit(rate.Limit(bps))
	l.SetBurst(burst)
}

func limiterToBytesPerSec(l *rate.Limiter) int64 {
	if l == nil || l.Limit() == rate.Inf {
		return 0
	}
	return int64(l.Limit())
}
