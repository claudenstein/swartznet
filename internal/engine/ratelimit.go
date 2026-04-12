package engine

import "golang.org/x/time/rate"

// UploadLimitBytesPerSec returns the current upload bandwidth cap
// in bytes per second. Zero means unlimited.
func (e *Engine) UploadLimitBytesPerSec() int64 {
	return limiterToBytesPerSec(e.ulLimiter)
}

// DownloadLimitBytesPerSec returns the current download bandwidth
// cap in bytes per second. Zero means unlimited.
func (e *Engine) DownloadLimitBytesPerSec() int64 {
	return limiterToBytesPerSec(e.dlLimiter)
}

// SetUploadLimitBytesPerSec caps outbound throughput. Pass 0 (or
// negative) to disable the cap. Takes effect immediately for
// every active connection via the shared rate.Limiter.
func (e *Engine) SetUploadLimitBytesPerSec(bps int64) {
	setLimiterBytesPerSec(e.ulLimiter, bps)
	e.log.Info("engine.upload_limit_set", "bytes_per_sec", bps)
}

// SetDownloadLimitBytesPerSec caps inbound throughput. Pass 0 (or
// negative) to disable the cap.
func (e *Engine) SetDownloadLimitBytesPerSec(bps int64) {
	setLimiterBytesPerSec(e.dlLimiter, bps)
	e.log.Info("engine.download_limit_set", "bytes_per_sec", bps)
}

// limiterToBytesPerSec inverts setLimiterBytesPerSec. Unlimited
// limiter (rate.Inf) returns 0.
func limiterToBytesPerSec(l *rate.Limiter) int64 {
	if l == nil {
		return 0
	}
	lim := l.Limit()
	if lim == rate.Inf {
		return 0
	}
	return int64(lim)
}

// setLimiterBytesPerSec mutates an existing *rate.Limiter in place
// so every goroutine holding a reference sees the new rate. A
// non-positive bps sets the limiter to rate.Inf (unlimited).
//
// Burst is set to the rate (one second of tokens) which mirrors
// anacrolix's own cmd/torrent defaults and gives the limiter
// enough headroom to absorb single-chunk reservations without
// starving the I/O loop.
func setLimiterBytesPerSec(l *rate.Limiter, bps int64) {
	if l == nil {
		return
	}
	if bps <= 0 {
		l.SetLimit(rate.Inf)
		l.SetBurst(0)
		return
	}
	l.SetLimit(rate.Limit(bps))
	// Burst must be ≥ the largest single reservation anacrolix
	// ever makes. In practice that's a piece chunk (16 KiB
	// default), but aligning burst to the per-second rate makes
	// the cap easier to reason about for users.
	burst := int(bps)
	if burst < 16*1024 {
		burst = 16 * 1024
	}
	l.SetBurst(burst)
}
