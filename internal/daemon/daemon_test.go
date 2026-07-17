package daemon

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/config"
)

func testConfig(t *testing.T) config.Config {
	t.Helper()
	tmp := t.TempDir()
	return config.Config{
		DataDir:  filepath.Join(tmp, "data"),
		IndexDir: filepath.Join(tmp, "index"),
	}
}

// eventLog collects ordered events from both code and slog, race-safely.
type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (e *eventLog) add(s string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, s)
}

func (e *eventLog) list() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.events...)
}

func (e *eventLog) Enabled(context.Context, slog.Level) bool { return true }
func (e *eventLog) Handle(_ context.Context, r slog.Record) error {
	e.add("log:" + r.Message)
	return nil
}
func (e *eventLog) WithAttrs([]slog.Attr) slog.Handler { return e }
func (e *eventLog) WithGroup(string) slog.Handler      { return e }

func TestNewAndStatusRoundTrip(t *testing.T) {
	d, err := New(context.Background(), Options{
		Cfg:     testConfig(t),
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		APIAddr: "localhost:0",
		Version: "vTEST",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.API == nil {
		t.Fatal("API not started")
	}
	resp, err := http.Get("http://" + d.API.Addr() + "/status")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestEmptyAPIAddrDisablesAPI(t *testing.T) {
	d, err := New(context.Background(), Options{
		Cfg: testConfig(t),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.API != nil {
		t.Fatal("empty APIAddr must disable the API (empty-path = feature-off)")
	}
}

func TestInvalidConfigAborts(t *testing.T) {
	_, err := New(context.Background(), Options{
		Cfg: config.Config{DataDir: ""},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err == nil || !strings.Contains(err.Error(), "DataDir") {
		t.Fatalf("err = %v, want config rejection", err)
	}
}

func TestAPIBindFailureDegrades(t *testing.T) {
	// Occupy a port so the daemon's bind fails.
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var stderr bytes.Buffer
	d, err := New(context.Background(), Options{
		Cfg:     testConfig(t),
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		APIAddr: ln.Addr().String(),
		Stderr:  &stderr,
	})
	if err != nil {
		t.Fatalf("bind failure must degrade, not abort: %v", err)
	}
	defer d.Close()
	if d.API != nil {
		t.Fatal("API must be nil after bind failure")
	}
	if !strings.Contains(stderr.String(), "warning: httpapi start failed:") {
		t.Fatalf("stderr = %q, want the degraded-start warning", stderr.String())
	}
}

func TestNilStderrDoesNotPanic(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	d, err := New(context.Background(), Options{
		Cfg:     testConfig(t),
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		APIAddr: ln.Addr().String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
}

// TestCloseOrder pins the load-bearing teardown contract: the daemon-owned
// background context is cancelled and its goroutines joined strictly BEFORE
// any subsystem (here: the API) is stopped.
func TestCloseOrder(t *testing.T) {
	ev := &eventLog{}
	d, err := New(context.Background(), Options{
		Cfg:     testConfig(t),
		Log:     slog.New(ev),
		APIAddr: "localhost:0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.API == nil {
		t.Fatal("API not started")
	}
	addr := d.API.Addr()

	started := make(chan struct{})
	d.goBG(func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		time.Sleep(20 * time.Millisecond) // force Close to actually wait
		ev.add("bg-exited")
	})
	<-started

	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	events := ev.list()
	idx := func(name string) int {
		for i, e := range events {
			if e == name {
				return i
			}
		}
		t.Fatalf("event %q missing from %v", name, events)
		return -1
	}
	begin := idx("log:daemon.close_begin")
	bgExited := idx("bg-exited")
	joined := idx("log:daemon.bg_joined")
	apiStopped := idx("log:httpapi.stopped")
	done := idx("log:daemon.close_done")
	if !(begin < bgExited && bgExited < joined && joined < apiStopped && apiStopped < done) {
		t.Fatalf("teardown order wrong: %v", events)
	}

	// The API must actually be down.
	if resp, err := http.Get("http://" + addr + "/status"); err == nil {
		resp.Body.Close()
		t.Fatal("API still serving after Close")
	}
}

// TestGoBGAfterCloseIsNoOp pins the registration/teardown race fix: goBG
// concurrent with or after Close must never spawn a goroutine the join
// cannot see (a bare Add racing Wait is a data race and an unjoined leak).
func TestGoBGAfterCloseIsNoOp(t *testing.T) {
	d, err := New(context.Background(), Options{
		Cfg: testConfig(t),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Hammer registration concurrently with Close; -race guards the Add/Wait pair.
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.goBG(func(ctx context.Context) { <-ctx.Done() })
		}()
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()

	ran := make(chan struct{})
	d.goBG(func(context.Context) { close(ran) })
	select {
	case <-ran:
		t.Fatal("goBG after Close must be a no-op")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestCloseIdempotent(t *testing.T) {
	ev := &eventLog{}
	d, err := New(context.Background(), Options{
		Cfg:     testConfig(t),
		Log:     slog.New(ev),
		APIAddr: "localhost:0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("repeated Close must return the cached (nil) error, got %v", err)
	}
	var begins int
	for _, e := range ev.list() {
		if e == "log:daemon.close_begin" {
			begins++
		}
	}
	if begins != 1 {
		t.Fatalf("teardown ran %d times, want once", begins)
	}
}
