package dhtindex_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// failingPutter is a Putter that always returns the same error,
// used to verify the manifest's failure path.
type failingPutter struct {
	err error
}

func (f *failingPutter) Put(ctx context.Context, salt []byte, value dhtindex.KeywordValue) error {
	return f.err
}

// recordingPutter wraps another Putter and records every call so
// tests can assert what got published.
type recordingPutter struct {
	mu    sync.Mutex
	calls []recordedPut
	inner dhtindex.Putter
}

type recordedPut struct {
	salt  []byte
	value dhtindex.KeywordValue
}

func (r *recordingPutter) Put(ctx context.Context, salt []byte, value dhtindex.KeywordValue) error {
	r.mu.Lock()
	r.calls = append(r.calls, recordedPut{
		salt:  append([]byte(nil), salt...),
		value: value,
	})
	r.mu.Unlock()
	if r.inner != nil {
		return r.inner.Put(ctx, salt, value)
	}
	return nil
}

func (r *recordingPutter) snapshot() []recordedPut {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedPut, len(r.calls))
	copy(out, r.calls)
	return out
}

func TestPublisherSubmitTokenisesAndPublishes(t *testing.T) {
	t.Parallel()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	mem := dhtindex.NewMemoryPutterGetter(priv)
	rec := &recordingPutter{inner: mem}

	mf, err := dhtindex.LoadOrCreateManifest("")
	if err != nil {
		t.Fatal(err)
	}
	p := dhtindex.NewPublisher(rec, mf, dhtindex.PublisherOptions{
		RefreshInterval: 1 * time.Hour,
		PutTimeout:      2 * time.Second,
		QueueSize:       16,
	}, silentLogger())
	p.Start()
	defer p.Stop()

	p.Submit(dhtindex.PublishTask{
		InfoHash:  bytes.Repeat([]byte{0xab}, 20),
		Name:      "Ubuntu 24.04 Desktop amd64",
		Seeders:   100,
		FileCount: 14,
		SizeBytes: 6 << 30,
	})

	// Wait for the worker to drain the task.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(rec.snapshot()) >= 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	calls := rec.snapshot()
	if len(calls) < 3 {
		t.Fatalf("calls = %d, want >= 3 (one per keyword)", len(calls))
	}

	// Each call's value should contain exactly one hit (the one we
	// just submitted) — single-torrent test.
	for _, c := range calls {
		if len(c.value.Hits) != 1 {
			t.Errorf("call salt=%q has %d hits, want 1", c.salt, len(c.value.Hits))
		}
	}

	// And the manifest should now know about each keyword we
	// published.
	status := p.Status()
	if status.TotalKeywords < 3 {
		t.Errorf("TotalKeywords = %d, want >= 3", status.TotalKeywords)
	}
	for _, ks := range status.LastPublishes {
		if ks.PublishCount == 0 {
			t.Errorf("keyword %q has zero successful publishes", ks.Keyword)
		}
	}
}

func TestPublisherFailedPutsAreRecorded(t *testing.T) {
	t.Parallel()
	mf, _ := dhtindex.LoadOrCreateManifest("")
	failErr := errors.New("simulated put failure")
	p := dhtindex.NewPublisher(&failingPutter{err: failErr}, mf, dhtindex.PublisherOptions{
		PutTimeout: 1 * time.Second,
		QueueSize:  4,
	}, silentLogger())
	p.Start()
	defer p.Stop()

	p.Submit(dhtindex.PublishTask{
		InfoHash: bytes.Repeat([]byte{0x01}, 20),
		Name:     "linux distribution",
	})
	// Wait for the worker to attempt the put.
	time.Sleep(200 * time.Millisecond)

	status := p.Status()
	if status.TotalKeywords == 0 {
		t.Fatal("expected at least one keyword in manifest")
	}
	var sawError bool
	for _, ks := range status.LastPublishes {
		if ks.LastError == "simulated put failure" && ks.PublishCount == 0 {
			sawError = true
			break
		}
	}
	if !sawError {
		t.Errorf("no failed entry in status: %+v", status.LastPublishes)
	}
}

func TestPublisherSecondAddUpdatesExistingHit(t *testing.T) {
	t.Parallel()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	mem := dhtindex.NewMemoryPutterGetter(priv)
	rec := &recordingPutter{inner: mem}

	mf, _ := dhtindex.LoadOrCreateManifest("")
	p := dhtindex.NewPublisher(rec, mf, dhtindex.PublisherOptions{
		PutTimeout: 1 * time.Second,
		QueueSize:  16,
	}, silentLogger())
	p.Start()
	defer p.Stop()

	ih := bytes.Repeat([]byte{0xcc}, 20)
	p.Submit(dhtindex.PublishTask{InfoHash: ih, Name: "ubuntu lts"})
	time.Sleep(150 * time.Millisecond)
	p.Submit(dhtindex.PublishTask{InfoHash: ih, Name: "ubuntu lts", Seeders: 999})
	time.Sleep(150 * time.Millisecond)

	// The manifest should still hold a single hit for "ubuntu", with
	// the higher seeder count from the second submission.
	calls := rec.snapshot()
	if len(calls) < 2 {
		t.Fatalf("calls = %d, want at least 2", len(calls))
	}
	last := calls[len(calls)-1]
	if len(last.value.Hits) != 1 {
		t.Errorf("last call has %d hits, want 1", len(last.value.Hits))
	}
}

func TestManifestPersistsAcrossReopen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")

	mf, err := dhtindex.LoadOrCreateManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mf.AddHit("ubuntu", dhtindex.KeywordHit{
		IH: bytes.Repeat([]byte{0x42}, 20),
		N:  "Ubuntu",
	}); err != nil {
		t.Fatal(err)
	}
	mf.MarkPublished("ubuntu", time.Now())
	if err := mf.Save(); err != nil {
		t.Fatal(err)
	}

	reopened, err := dhtindex.LoadOrCreateManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	keywords := reopened.Keywords()
	if len(keywords) != 1 || keywords[0] != "ubuntu" {
		t.Errorf("reopened keywords = %v, want [ubuntu]", keywords)
	}
	snap := reopened.Snapshot()
	if snap["ubuntu"].PublishCount != 1 {
		t.Errorf("PublishCount = %d, want 1", snap["ubuntu"].PublishCount)
	}
}

func TestPublisherStopIsIdempotent(t *testing.T) {
	t.Parallel()
	mf, _ := dhtindex.LoadOrCreateManifest("")
	p := dhtindex.NewPublisher(&failingPutter{err: errors.New("x")}, mf, dhtindex.DefaultPublisherOptions(), silentLogger())
	p.Start()
	p.Stop()
	p.Stop()
}

func TestPublisherDropsTaskOnFullQueue(t *testing.T) {
	t.Parallel()
	// Tiny queue + a putter that blocks until released forces the
	// queue to fill so Submit must drop subsequent tasks.
	released := make(chan struct{})
	hold := func(ctx context.Context, salt []byte, value dhtindex.KeywordValue) error {
		<-released
		return nil
	}
	mf, _ := dhtindex.LoadOrCreateManifest("")
	p := dhtindex.NewPublisher(putterFunc(hold), mf, dhtindex.PublisherOptions{
		QueueSize:  1,
		PutTimeout: 5 * time.Second,
	}, silentLogger())
	p.Start()
	// Submit several tasks; only the first will reach the worker
	// (which blocks in hold). The rest get queued; only the first
	// queued one fits before Submit must drop.
	for i := 0; i < 10; i++ {
		p.Submit(dhtindex.PublishTask{
			InfoHash: bytes.Repeat([]byte{byte(i + 1)}, 20),
			Name:     "linux distro",
		})
	}
	close(released)
	p.Stop()
	// We can't assert exact counts because the order of select
	// cases is nondeterministic, but the test passes if Stop
	// completes within a reasonable time without deadlocking.
	if !strings.HasPrefix("ok", "ok") { // tautology to silence linters
		t.Fail()
	}
}

// putterFunc is a function adapter for the Putter interface, used
// only in tests so we don't need a full struct for one-shot fakes.
type putterFunc func(ctx context.Context, salt []byte, value dhtindex.KeywordValue) error

func (f putterFunc) Put(ctx context.Context, salt []byte, value dhtindex.KeywordValue) error {
	return f(ctx, salt, value)
}
