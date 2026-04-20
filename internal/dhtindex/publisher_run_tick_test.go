package dhtindex

import (
	"bytes"
	"io"
	"log/slog"
	"testing"
	"time"
)

// TestPublisherRunRefreshTickFires covers the
// `<-tick.C → p.refreshAll(ctx)` branch of dhtindex.Publisher.run.
// Use a 50ms RefreshInterval and observe that the recordingPutter
// receives at least one Put call from the tick-driven refreshAll
// (no Submit was called, so Put can only originate from the tick).
func TestPublisherRunRefreshTickFires(t *testing.T) {
	t.Parallel()
	mf, _ := LoadOrCreateManifest("")
	if _, err := mf.AddHit("ubuntu", KeywordHit{IH: bytes.Repeat([]byte{1}, 20), N: "u"}); err != nil {
		t.Fatal(err)
	}

	put := &recordingPutter{}
	p := NewPublisher(put, mf, PublisherOptions{
		RefreshInterval: 50 * time.Millisecond,
		PutTimeout:      1 * time.Second,
		QueueSize:       4,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.Start()
	defer p.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if put.calls.Load() >= 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("Put never fired from tick; calls = %d", put.calls.Load())
}
