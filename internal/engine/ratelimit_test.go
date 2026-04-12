package engine_test

import "testing"

func TestRateLimitDefaultsUnlimited(t *testing.T) {
	t.Parallel()
	eng := newTestEngine(t)
	if got := eng.UploadLimitBytesPerSec(); got != 0 {
		t.Errorf("upload default: got %d, want 0 (unlimited)", got)
	}
	if got := eng.DownloadLimitBytesPerSec(); got != 0 {
		t.Errorf("download default: got %d, want 0 (unlimited)", got)
	}
}

func TestRateLimitSetAndGet(t *testing.T) {
	t.Parallel()
	eng := newTestEngine(t)

	eng.SetUploadLimitBytesPerSec(500_000)
	if got := eng.UploadLimitBytesPerSec(); got != 500_000 {
		t.Errorf("after set 500K: got %d", got)
	}

	eng.SetDownloadLimitBytesPerSec(1_000_000)
	if got := eng.DownloadLimitBytesPerSec(); got != 1_000_000 {
		t.Errorf("after set 1M: got %d", got)
	}

	// Zero disables.
	eng.SetUploadLimitBytesPerSec(0)
	if got := eng.UploadLimitBytesPerSec(); got != 0 {
		t.Errorf("after set 0: got %d", got)
	}

	// Negative also disables (and doesn't panic).
	eng.SetDownloadLimitBytesPerSec(-1)
	if got := eng.DownloadLimitBytesPerSec(); got != 0 {
		t.Errorf("after set -1: got %d", got)
	}
}
