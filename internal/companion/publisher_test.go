package companion_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/swartznet/swartznet/internal/companion"
)

// fakePutter records every PutInfohashPointer call so the
// publisher tests can assert what got published. The infohash
// captured is the last one written.
type fakePutter struct {
	mu       sync.Mutex
	calls    int
	lastSalt []byte
	lastIH   [20]byte
	failWith error
}

func (f *fakePutter) PutInfohashPointer(ctx context.Context, salt []byte, infohash [20]byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastSalt = append([]byte(nil), salt...)
	f.lastIH = infohash
	return f.failWith
}

func (f *fakePutter) snapshot() (int, [20]byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.lastIH
}

// fakeSeeder records every AddTorrentMetaInfo call. Returning
// an error simulates anacrolix's "duplicate infohash" complaint
// — the publisher must swallow it rather than treating it as a
// failure.
type fakeSeeder struct {
	calls    atomic.Int64
	failWith error
}

func (f *fakeSeeder) AddTorrentMetaInfo(mi *metainfo.MetaInfo) (any, error) {
	f.calls.Add(1)
	if f.failWith != nil {
		return nil, f.failWith
	}
	return struct{}{}, nil
}

// silentLogger discards all log output so the test runs are
// not noisy.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestPublisherRefreshHappyPath(t *testing.T) {
	t.Parallel()
	idx := seedIndex(t)
	dir := t.TempDir()
	put := &fakePutter{}
	seed := &fakeSeeder{}

	opts := companion.DefaultPublisherOptions()
	opts.Dir = dir
	opts.Interval = time.Hour    // we trigger manually
	opts.MinInterval = time.Nanosecond // do not throttle inside the test
	opts.PutTimeout = 5 * time.Second

	p, err := companion.NewPublisher(idx, put, seed, opts, silentLogger())
	if err != nil {
		t.Fatal(err)
	}
	p.Start()
	defer p.Stop()

	// The initial refresh runs from p.run() inside Start. Wait for
	// it to complete by polling the publisher status.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if put.snapshotCalls() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	calls, ih := put.snapshot()
	if calls == 0 {
		t.Fatal("PutInfohashPointer was never called")
	}
	var zero [20]byte
	if ih == zero {
		t.Fatal("published infohash is all zeros")
	}
	if seed.calls.Load() == 0 {
		t.Fatal("AddTorrentMetaInfo was never called")
	}

	st := p.Status()
	if st.LastError != "" {
		t.Errorf("LastError = %q, want empty", st.LastError)
	}
	if st.LastInfoHash == "" {
		t.Errorf("LastInfoHash is empty")
	}
	if st.PublishedCount == 0 {
		t.Errorf("PublishedCount = 0, want >0")
	}
}

func TestPublisherRefreshNowTriggers(t *testing.T) {
	t.Parallel()
	idx := seedIndex(t)
	dir := t.TempDir()
	put := &fakePutter{}
	seed := &fakeSeeder{}

	opts := companion.DefaultPublisherOptions()
	opts.Dir = dir
	opts.Interval = time.Hour
	opts.MinInterval = time.Nanosecond

	p, err := companion.NewPublisher(idx, put, seed, opts, silentLogger())
	if err != nil {
		t.Fatal(err)
	}
	p.Start()
	defer p.Stop()

	// Wait for the initial refresh.
	waitForCalls(t, put, 1, 2*time.Second)

	if err := p.RefreshNow(); err != nil {
		t.Fatalf("RefreshNow: %v", err)
	}
	waitForCalls(t, put, 2, 2*time.Second)

	calls, _ := put.snapshot()
	if calls < 2 {
		t.Errorf("calls = %d, want >=2", calls)
	}
}

func TestPublisherSeederErrorIsBenign(t *testing.T) {
	t.Parallel()
	idx := seedIndex(t)
	dir := t.TempDir()
	put := &fakePutter{}
	seed := &fakeSeeder{failWith: errors.New("duplicate infohash")}

	opts := companion.DefaultPublisherOptions()
	opts.Dir = dir
	opts.Interval = time.Hour
	opts.MinInterval = time.Nanosecond

	p, err := companion.NewPublisher(idx, put, seed, opts, silentLogger())
	if err != nil {
		t.Fatal(err)
	}
	p.Start()
	defer p.Stop()

	waitForCalls(t, put, 1, 2*time.Second)

	st := p.Status()
	if st.LastError != "" {
		t.Errorf("seeder error should not become a failure: LastError = %q", st.LastError)
	}
}

func TestPublisherPutErrorRecorded(t *testing.T) {
	t.Parallel()
	idx := seedIndex(t)
	dir := t.TempDir()
	put := &fakePutter{failWith: errors.New("dht unreachable")}
	seed := &fakeSeeder{}

	opts := companion.DefaultPublisherOptions()
	opts.Dir = dir
	opts.Interval = time.Hour
	opts.MinInterval = time.Nanosecond

	p, err := companion.NewPublisher(idx, put, seed, opts, silentLogger())
	if err != nil {
		t.Fatal(err)
	}
	p.Start()
	defer p.Stop()

	waitForCalls(t, put, 1, 2*time.Second)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p.Status().LastError != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if p.Status().LastError == "" {
		t.Error("expected LastError to be recorded after a put failure")
	}
	if p.Status().PublishedCount != 0 {
		t.Errorf("PublishedCount = %d, want 0 after put failure", p.Status().PublishedCount)
	}
}

func TestPublisherRefreshNowThrottled(t *testing.T) {
	t.Parallel()
	idx := seedIndex(t)
	dir := t.TempDir()
	put := &fakePutter{}
	seed := &fakeSeeder{}

	opts := companion.DefaultPublisherOptions()
	opts.Dir = dir
	opts.Interval = time.Hour
	opts.MinInterval = time.Hour // make sure throttling kicks in

	p, err := companion.NewPublisher(idx, put, seed, opts, silentLogger())
	if err != nil {
		t.Fatal(err)
	}
	p.Start()
	defer p.Stop()

	waitForCalls(t, put, 1, 2*time.Second)

	if err := p.RefreshNow(); !errors.Is(err, companion.ErrTooSoon) {
		t.Errorf("RefreshNow err = %v, want ErrTooSoon", err)
	}
}

func TestNewPublisherValidatesArgs(t *testing.T) {
	t.Parallel()
	idx := seedIndex(t)
	put := &fakePutter{}
	seed := &fakeSeeder{}
	good := companion.PublisherOptions{Dir: t.TempDir()}

	if _, err := companion.NewPublisher(nil, put, seed, good, silentLogger()); err == nil {
		t.Error("expected error for nil index")
	}
	if _, err := companion.NewPublisher(idx, nil, seed, good, silentLogger()); err == nil {
		t.Error("expected error for nil putter")
	}
	if _, err := companion.NewPublisher(idx, put, nil, good, silentLogger()); err == nil {
		t.Error("expected error for nil seeder")
	}
	if _, err := companion.NewPublisher(idx, put, seed, companion.PublisherOptions{}, silentLogger()); err == nil {
		t.Error("expected error for empty Dir")
	}
}

// snapshotCalls is a tiny accessor used by the wait loop to
// poll the call count without holding the lock for the whole
// retry window.
func (f *fakePutter) snapshotCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func waitForCalls(t *testing.T, p *fakePutter, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if p.snapshotCalls() >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("PutInfohashPointer call count never reached %d (have %d)", want, p.snapshotCalls())
}
