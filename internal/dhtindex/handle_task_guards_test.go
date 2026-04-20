package dhtindex_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// TestPublisherSubmitBadInfoHashIgnored covers handleTask's
// `len(task.InfoHash) != 20` early-return branch. Submit a task
// with a 19-byte infohash; the worker must log+skip rather than
// add anything to the manifest.
func TestPublisherSubmitBadInfoHashIgnored(t *testing.T) {
	t.Parallel()
	mf, _ := dhtindex.LoadOrCreateManifest("")
	rec := &recordingPutter{}
	p := dhtindex.NewPublisher(rec, mf, dhtindex.PublisherOptions{
		PutTimeout: 1 * time.Second,
		QueueSize:  4,
	}, silentLogger())
	p.Start()
	defer p.Stop()

	p.Submit(dhtindex.PublishTask{
		InfoHash: bytes.Repeat([]byte{0xab}, 19), // wrong length
		Name:     "Ubuntu 24.04 Desktop",
	})

	// Give the worker a moment to drain (and ignore) the task.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(rec.snapshot()) > 0 {
			t.Fatalf("recordingPutter saw a put for a bad-length infohash: %+v", rec.snapshot())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if status := p.Status(); status.TotalKeywords != 0 {
		t.Errorf("TotalKeywords = %d, want 0 (bad infohash should be skipped)", status.TotalKeywords)
	}
}

// TestPublisherSubmitEmptyTokenizationIgnored covers handleTask's
// `len(keywords) == 0` early-return branch. Submit a task whose
// Name is whitespace-only (so Tokenize returns nothing); the
// worker must drop it without recording any put.
func TestPublisherSubmitEmptyTokenizationIgnored(t *testing.T) {
	t.Parallel()
	mf, _ := dhtindex.LoadOrCreateManifest("")
	rec := &recordingPutter{}
	p := dhtindex.NewPublisher(rec, mf, dhtindex.PublisherOptions{
		PutTimeout: 1 * time.Second,
		QueueSize:  4,
	}, silentLogger())
	p.Start()
	defer p.Stop()

	p.Submit(dhtindex.PublishTask{
		InfoHash: bytes.Repeat([]byte{0xfe}, 20),
		Name:     "   ", // tokenizer returns no keywords
	})

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(rec.snapshot()) > 0 {
			t.Fatalf("recordingPutter saw a put for empty-tokenization: %+v", rec.snapshot())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if status := p.Status(); status.TotalKeywords != 0 {
		t.Errorf("TotalKeywords = %d, want 0", status.TotalKeywords)
	}
}
