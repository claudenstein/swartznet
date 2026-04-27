package dhtindex

import (
	"bytes"
	"io"
	"log/slog"
	"testing"
	"time"
)

// TestPublisherRunReturnsOnClosedTasks covers Publisher.run's
// `case task, ok := <-p.tasks: if !ok { return }` arm at
// publisher.go:174-177. Production callers signal shutdown via
// Stop()/stopCh, but the explicit !ok guard handles a future
// caller that closes p.tasks directly. Force-close the channel
// via the internal field so run exits on the closed-channel read.
func TestPublisherRunReturnsOnClosedTasks(t *testing.T) {
	t.Parallel()
	mf, _ := LoadOrCreateManifest("")
	if _, err := mf.AddHit("ubuntu", KeywordHit{IH: bytes.Repeat([]byte{1}, 20), N: "u"}); err != nil {
		t.Fatal(err)
	}
	put := &recordingPutter{}
	p := NewPublisher(put, mf, PublisherOptions{
		RefreshInterval: time.Hour, // tick won't fire during test
		PutTimeout:      1 * time.Second,
		QueueSize:       4,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.Start()

	// Close the tasks channel directly. run's select picks the
	// closed-channel case and returns. Stop's wg.Wait then
	// completes; we don't call Stop because closing stopCh after
	// run already exited would be a no-op (sync.Once-guarded).
	close(p.tasks)

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publisher.run did not exit on closed tasks within 2s")
	}
}
