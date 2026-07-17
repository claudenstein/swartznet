package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

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

func serveArgs(t *testing.T, extra ...string) []string {
	t.Helper()
	tmp := t.TempDir()
	args := []string{
		"--data-dir", filepath.Join(tmp, "data"),
		"--index-dir", filepath.Join(tmp, "index"),
	}
	return append(args, extra...)
}

// startServe runs serveWithContext in a goroutine and waits for the API
// address to appear on stdout. Cancel via the returned cancel; the exit code
// arrives on codeCh.
func startServe(t *testing.T, args []string) (addr string, stdout, stderr *syncBuffer, cancel context.CancelFunc, codeCh chan int) {
	t.Helper()
	t.Setenv("SWARTZNET_LOG", "") // teardown assertions read Info-level output
	ctx, cancelFn := context.WithCancel(context.Background())
	stdout, stderr = &syncBuffer{}, &syncBuffer{}
	codeCh = make(chan int, 1)
	go func() {
		codeCh <- serveWithContext(ctx, args, stdout, stderr)
	}()
	deadline := time.Now().Add(5 * time.Second)
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
			t.Fatalf("serve did not report a listening address; stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	return addr, stdout, stderr, cancelFn, codeCh
}

func TestServeLifecycle(t *testing.T) {
	addr, stdout, _, cancel, codeCh := startServe(t, serveArgs(t, "--api-addr", "localhost:0"))

	// The daemon answers while running.
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

	// Cancellation (the signal path's effect) produces a clean 130 exit.
	cancel()
	select {
	case code := <-codeCh:
		if code != exitInterrupt {
			t.Fatalf("exit = %d, want %d", code, exitInterrupt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not exit after cancel")
	}
	if !strings.Contains(stdout.String(), "Shutting down...") {
		t.Fatalf("stdout %q lacks shutdown line", stdout.String())
	}

	// The API must be down after exit.
	if resp, err := http.Get("http://" + addr + "/status"); err == nil {
		resp.Body.Close()
		t.Fatal("API still serving after serve exited")
	}
}

func TestServeTeardownOrderInLogs(t *testing.T) {
	args := serveArgs(t, "--api-addr", "localhost:0")
	_, _, stderr, cancel, codeCh := startServe(t, args)
	cancel()
	<-codeCh
	logs := stderr.String()
	order := []string{"daemon.close_begin", "daemon.bg_joined", "httpapi.stopped", "daemon.close_done"}
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

func TestServeAPIDisabled(t *testing.T) {
	t.Setenv("SWARTZNET_LOG", "")
	ctx, cancel := context.WithCancel(context.Background())
	stdout, stderr := &syncBuffer{}, &syncBuffer{}
	codeCh := make(chan int, 1)
	go func() {
		codeCh <- serveWithContext(ctx, serveArgs(t, "--api-addr", ""), stdout, stderr)
	}()
	// Give it a moment to (not) bind, then stop.
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case code := <-codeCh:
		if code != exitInterrupt {
			t.Fatalf("exit = %d, want 130; stderr=%q", code, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not exit")
	}
	if strings.Contains(stdout.String(), "HTTP API listening") {
		t.Fatalf("--api-addr \"\" must not start the API; stdout=%q", stdout.String())
	}
	if strings.Contains(stderr.String(), "httpapi.listening") {
		t.Fatalf("--api-addr \"\" must not log a listener; stderr=%q", stderr.String())
	}
}

func TestServeBindFailure(t *testing.T) {
	t.Setenv("SWARTZNET_LOG", "")
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var stdout, stderr syncBuffer
	code := serveWithContext(context.Background(), serveArgs(t, "--api-addr", ln.Addr().String()), &stdout, &stderr)
	if code != exitRuntime {
		t.Fatalf("exit = %d, want 1", code)
	}
	errOut := stderr.String()
	if !strings.Contains(errOut, "warning: httpapi start failed:") {
		t.Fatalf("stderr %q lacks the degraded-start warning", errOut)
	}
	if !strings.Contains(errOut, "swartznet: http api did not start") {
		t.Fatalf("stderr %q lacks the scaffold failure line", errOut)
	}
}

// TestCmdServeRealSignalPath drives cmdServe itself — signalContext included —
// by delivering real signals to the test process. This is the only automated
// guard on the DoD's headline exit-130 contract; the other lifecycle tests
// substitute a test-owned cancel.
func TestCmdServeRealSignalPath(t *testing.T) {
	for _, tc := range []struct {
		name string
		sig  syscall.Signal
	}{
		{"SIGINT", syscall.SIGINT},
		{"SIGTERM", syscall.SIGTERM},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SWARTZNET_LOG", "")
			stdout, stderr := &syncBuffer{}, &syncBuffer{}
			codeCh := make(chan int, 1)
			args := serveArgs(t, "--api-addr", "localhost:0")
			go func() {
				codeCh <- cmdServe(args, stdout, stderr)
			}()
			deadline := time.Now().Add(5 * time.Second)
			for !strings.Contains(stdout.String(), "HTTP API listening on ") {
				if time.Now().After(deadline) {
					t.Fatalf("serve did not start; stderr=%q", stderr.String())
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
			case <-time.After(5 * time.Second):
				t.Fatalf("cmdServe did not exit after %s", tc.name)
			}
			if !strings.Contains(stdout.String(), "Shutting down...") {
				t.Fatalf("stdout %q lacks shutdown line", stdout.String())
			}
			if !strings.Contains(stderr.String(), "daemon.close_done") {
				t.Fatalf("stderr %q lacks teardown completion", stderr.String())
			}
		})
	}
}

func TestServeBadConfig(t *testing.T) {
	tmp := t.TempDir()
	blocker := filepath.Join(tmp, "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr syncBuffer
	code := serveWithContext(context.Background(), []string{
		"--data-dir", filepath.Join(blocker, "data"),
		"--api-addr", "localhost:0",
	}, &stdout, &stderr)
	if code != exitRuntime {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "swartznet: config: cannot create DataDir") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
