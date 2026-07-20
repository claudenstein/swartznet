package main

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/identity"
)

// dummyInfohash is a valid-but-unfindable infohash: with --no-dht and no
// peers, metadata never arrives and the daemon idles — the lifecycle-test
// stand-in for the deleted serve scaffold.
const dummyInfohash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// syncBuffer is a race-safe bytes.Buffer for goroutine-crossing writers.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func addArgs(t *testing.T, extra ...string) []string {
	t.Helper()
	tmp := t.TempDir()
	// Hermetic: temp XDG root (identity auto-create), OS-assigned BT port,
	// DHT off — tests must never touch the operator's state or network.
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "xdg"))
	t.Setenv("SWARTZNET_LOG", "")
	args := []string{
		"--data-dir", filepath.Join(tmp, "data"),
		"--index-dir", filepath.Join(tmp, "index"),
		"--port", "0",
		"--no-dht",
		"--no-index", // lifecycle tests don't exercise Layer L; skip Bleve open
	}
	return append(args, extra...)
}

// startAdd runs addWithContext in a goroutine and waits for the API address.
func startAdd(t *testing.T, args []string, target string) (addr string, stdout, stderr *syncBuffer, cancel context.CancelFunc, codeCh chan int) {
	t.Helper()
	ctx, cancelFn := context.WithCancel(context.Background())
	stdout, stderr = &syncBuffer{}, &syncBuffer{}
	codeCh = make(chan int, 1)
	full := append(append([]string{}, args...), target)
	go func() {
		codeCh <- addWithContext(ctx, full, strings.NewReader(""), stdout, stderr)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		out := stdout.String()
		if i := strings.Index(out, "HTTP API listening on "); i >= 0 {
			rest := out[i+len("HTTP API listening on "):]
			if j := strings.IndexByte(rest, '\n'); j >= 0 {
				addr = rest[:j]
				break
			}
		}
		if time.Now().After(deadline) {
			cancelFn()
			t.Fatalf("add did not report a listening address; stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	return addr, stdout, stderr, cancelFn, codeCh
}

func TestAddLifecycle(t *testing.T) {
	addr, stdout, _, cancel, codeCh := startAdd(t, addArgs(t, "--api-addr", "localhost:0"), dummyInfohash)

	resp, err := http.Get("http://" + addr + "/status")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		cancel()
		t.Fatalf("status = %d", resp.StatusCode)
	}
	// The daemon knows its torrent.
	resp, err = http.Get("http://" + addr + "/torrents")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	body := new(bytes.Buffer)
	_, _ = body.ReadFrom(resp.Body)
	resp.Body.Close()
	if !strings.Contains(body.String(), dummyInfohash) {
		cancel()
		t.Fatalf("/torrents lacks the added infohash: %s", body.String())
	}

	cancel()
	select {
	case code := <-codeCh:
		if code != exitInterrupt {
			t.Fatalf("exit = %d, want %d", code, exitInterrupt)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("add did not exit after cancel")
	}
	if !strings.Contains(stdout.String(), "Fetching metadata for "+dummyInfohash) {
		t.Fatalf("stdout %q lacks metadata line", stdout.String())
	}
	// Ctrl-C during the metadata wait exits 130 with NO shutdown message
	// (legacy contract) — the line prints only from the progress loop.
	if strings.Contains(stdout.String(), "Shutting down...") {
		t.Fatalf("stdout %q has a shutdown line in the metadata-wait phase", stdout.String())
	}
	// API is down after exit.
	if resp, err := http.Get("http://" + addr + "/status"); err == nil {
		resp.Body.Close()
		t.Fatal("API still serving after add exited")
	}
}

func TestAddTeardownOrderInLogs(t *testing.T) {
	_, _, stderr, cancel, codeCh := startAdd(t, addArgs(t, "--api-addr", "localhost:0"), dummyInfohash)
	cancel()
	<-codeCh
	logs := stderr.String()
	order := []string{"daemon.close_begin", "daemon.bg_joined", "httpapi.stopped", "engine.stopped", "daemon.close_done"}
	last := -1
	for _, name := range order {
		i := strings.Index(logs, name)
		if i < 0 {
			t.Fatalf("teardown log %q missing; stderr=%q", name, logs)
		}
		if i < last {
			t.Fatalf("teardown log %q out of order; stderr=%q", name, logs)
		}
		last = i
	}
}

// TestCmdAddRealSignalPath drives cmdAdd itself — signalContext included —
// with real signals; the only automated guard on the exit-130 contract.
func TestCmdAddRealSignalPath(t *testing.T) {
	for _, tc := range []struct {
		name string
		sig  syscall.Signal
	}{
		{"SIGINT", syscall.SIGINT},
		{"SIGTERM", syscall.SIGTERM},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr := &syncBuffer{}, &syncBuffer{}
			codeCh := make(chan int, 1)
			args := append(addArgs(t, "--api-addr", "localhost:0"), dummyInfohash)
			go func() {
				codeCh <- cmdAdd(args, strings.NewReader(""), stdout, stderr)
			}()
			deadline := time.Now().Add(10 * time.Second)
			for !strings.Contains(stdout.String(), "HTTP API listening on ") {
				if time.Now().After(deadline) {
					t.Fatalf("add did not start; stderr=%q", stderr.String())
				}
				time.Sleep(5 * time.Millisecond)
			}
			if err := syscall.Kill(os.Getpid(), tc.sig); err != nil {
				t.Fatal(err)
			}
			select {
			case code := <-codeCh:
				if code != exitInterrupt {
					t.Fatalf("exit = %d, want %d", code, exitInterrupt)
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("cmdAdd did not exit after %s", tc.name)
			}
		})
	}
}

func TestAddUsageErrors(t *testing.T) {
	var stdout, stderr syncBuffer
	if code := addWithContext(context.Background(), []string{}, strings.NewReader(""), &stdout, &stderr); code != exitUsage {
		t.Fatalf("no target: exit = %d", code)
	}
	if !strings.Contains(stderr.String(), "usage: swartznet add <magnet | path.torrent | infohash | ->") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestAddUnsafeGate(t *testing.T) {
	t.Setenv("SWARTZNET_UNSAFE", "")
	var stdout, stderr syncBuffer
	code := addWithContext(context.Background(),
		append(addArgs(t, "--dht-insecure"), dummyInfohash),
		strings.NewReader(""), &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--dht-insecure disables BEP-42 node-ID security and is testing-only (set SWARTZNET_UNSAFE=1 to enable)") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestAddBadTorrentPathFails(t *testing.T) {
	var stdout, stderr syncBuffer
	code := addWithContext(context.Background(),
		append(addArgs(t, "--api-addr", ""), filepath.Join(t.TempDir(), "missing.torrent")),
		strings.NewReader(""), &stdout, &stderr)
	if code != exitRuntime {
		t.Fatalf("exit = %d, want 1; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "swartznet: engine: read .torrent:") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestAddIdentityFlagLoadOnly(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.key")
	var stdout, stderr syncBuffer
	code := addWithContext(context.Background(),
		append(addArgs(t, "--api-addr", "", "--identity", missing), dummyInfohash),
		strings.NewReader(""), &stdout, &stderr)
	if code != exitRuntime {
		t.Fatalf("exit = %d, want 1; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "swartznet: identity did not load") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("--identity minted a key at an explicit path")
	}
}

func TestAddIdentityFlagValid(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "elsewhere.key")
	id, err := identity.Load(keyFile, true)
	if err != nil {
		t.Fatal(err)
	}
	addr, _, _, cancel, codeCh := startAdd(t,
		addArgs(t, "--api-addr", "localhost:0", "--identity", keyFile), dummyInfohash)
	defer func() { cancel(); <-codeCh }()
	resp, err := http.Get("http://" + addr + "/status")
	if err != nil {
		t.Fatal(err)
	}
	body := new(bytes.Buffer)
	_, _ = body.ReadFrom(resp.Body)
	resp.Body.Close()
	if !strings.Contains(body.String(), id.PublicKeyHex()) {
		t.Fatalf("/status lacks the explicit key's pubkey: %s", body.String())
	}
}

// TestAddBindFailureDegrades: unlike the deleted serve scaffold, add keeps
// running when the API bind fails — the download is the point.
func TestAddBindFailureDegrades(t *testing.T) {
	ln, err := listenLoopback(t)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	stdout, stderr := &syncBuffer{}, &syncBuffer{}
	codeCh := make(chan int, 1)
	go func() {
		codeCh <- addWithContext(ctx,
			append(addArgs(t, "--api-addr", ln.Addr().String()), dummyInfohash),
			strings.NewReader(""), stdout, stderr)
	}()
	// The degraded node still reaches the metadata-fetch stage.
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(stdout.String(), "Fetching metadata for ") {
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("add did not keep running; stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.Contains(stderr.String(), "warning: httpapi start failed:") {
		cancel()
		t.Fatalf("stderr %q lacks the degraded-start warning", stderr.String())
	}
	cancel()
	if code := <-codeCh; code != exitInterrupt {
		t.Fatalf("exit = %d, want 130", code)
	}
}

func TestValidInfoHash(t *testing.T) {
	if !validInfoHash(dummyInfohash) {
		t.Fatal("valid hash rejected")
	}
	for _, bad := range []string{"", "short", strings.Repeat("g", 40), strings.Repeat("A", 40)} {
		if validInfoHash(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	for _, tc := range []struct {
		n    int64
		want string
	}{
		{512, "512 B"},
		{1536, "1.5 KiB"},
		{2 * 1024 * 1024, "2.0 MiB"},
		{3 * 1024 * 1024 * 1024, "3.0 GiB"},
	} {
		if got := humanBytes(tc.n); got != tc.want {
			t.Errorf("humanBytes(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}
