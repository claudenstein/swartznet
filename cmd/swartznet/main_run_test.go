package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestRunNoArgs covers run's `if len(args) == 0` arm — usage to
// stderr + exitUsage.
func TestRunNoArgs(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run(nil, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("no-args exit = %d, want exitUsage", code)
	}
	if !strings.Contains(stderr.String(), "swartznet") {
		t.Errorf("expected usage banner in stderr: %s", stderr.String())
	}
}

// TestRunHelp covers the `case "help"` arm — usage to stdout +
// exitOK.
func TestRunHelp(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run([]string{"help"}, &stdout, &stderr)
	if code != exitOK {
		t.Errorf("help exit = %d, want exitOK", code)
	}
	if !strings.Contains(stdout.String(), "Usage") {
		t.Errorf("expected 'Usage' in stdout: %s", stdout.String())
	}
}

// TestRunVersion covers the `case "version"` arm.
func TestRunVersion(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run([]string{"version"}, &stdout, &stderr)
	if code != exitOK {
		t.Errorf("version exit = %d, want exitOK", code)
	}
	if !strings.HasPrefix(stdout.String(), "swartznet ") {
		t.Errorf("expected 'swartznet ' prefix in stdout: %s", stdout.String())
	}
}

// TestRunUnknownCommand covers the `default:` arm of the
// command switch.
func TestRunUnknownCommand(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run([]string{"weird"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("unknown-cmd exit = %d, want exitUsage", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Errorf("expected 'unknown command' in stderr: %s", stderr.String())
	}
}

// TestRunDispatchesSubcommand smoke-tests one happy dispatch path:
// `aggregate help` → cmdAggregate → printAggregateUsage → exitOK.
func TestRunDispatchesSubcommand(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run([]string{"aggregate", "help"}, &stdout, &stderr)
	if code != exitOK {
		t.Errorf("aggregate-help exit = %d, want exitOK", code)
	}
}

// TestRunDispatchesEachSubcommand walks every case in run's
// switch by sending each subcommand bad/empty args so the arm is
// taken and the inner cmd returns an exit code without doing
// any HTTP. Asserts the exit code is one we recognise.
func TestRunDispatchesEachSubcommand(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		argv    []string
		wantNZ  bool // wantNZ=true → any non-zero is fine
		wantExt int  // exact match if wantNZ=false
	}{
		{"add", []string{"add"}, false, exitUsage},
		{"search", []string{"search"}, false, exitUsage},
		{"flag", []string{"flag"}, false, exitUsage},
		{"confirm", []string{"confirm"}, false, exitUsage},
		{"create", []string{"create"}, false, exitUsage},
		{"index", []string{"index"}, false, exitUsage},
		{"files", []string{"files"}, false, exitUsage},
		{"trust", []string{"trust"}, false, exitUsage},
		{"crawl-probe", []string{"crawl-probe"}, false, exitUsage},
		{"status", []string{"status", "--api-addr", "127.0.0.1:1"}, true, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			got := run(c.argv, &stdout, &stderr)
			if c.wantNZ {
				if got == exitOK {
					t.Errorf("%s: exit = %d, want non-zero", c.name, got)
				}
				return
			}
			if got != c.wantExt {
				t.Errorf("%s: exit = %d, want %d", c.name, got, c.wantExt)
			}
		})
	}
}

// TestPrintUsage covers printUsage directly.
func TestPrintUsage(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	printUsage(&buf)
	if !strings.Contains(buf.String(), "swartznet <command>") {
		t.Errorf("expected 'swartznet <command>' header: %s", buf.String())
	}
}

// TestNewLoggerEnvLevels covers newLogger's switch on
// SWARTZNET_LOG values: debug, warn, error, and unset (default
// info).
func TestNewLoggerEnvLevels(t *testing.T) {
	for _, lvl := range []string{"debug", "info", "warn", "error", "bogus"} {
		t.Run(lvl, func(t *testing.T) {
			t.Setenv("SWARTZNET_LOG", lvl)
			lg := newLogger(io.Discard)
			if lg == nil {
				t.Fatalf("newLogger(%q) returned nil", lvl)
			}
		})
	}
}

// TestSignalContextCancelsViaParent covers the
// `case <-ctx.Done(): … signal.Stop` arm of the inner goroutine —
// cancel the parent ctx and confirm the returned context fires.
func TestSignalContextCancelsViaParent(t *testing.T) {
	t.Parallel()
	parent, cancelParent := context.WithCancel(context.Background())
	ctx, cancel := signalContext(parent)
	defer cancel()

	cancelParent()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Error("ctx not cancelled within 1s after parent cancel")
	}
}

// TestReportRunErrNil covers reportRunErr's
// `if err == nil { return exitOK }` arm.
func TestReportRunErrNil(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	if got := reportRunErr(nil, &stderr); got != exitOK {
		t.Errorf("reportRunErr(nil) = %d, want exitOK", got)
	}
	if stderr.Len() != 0 {
		t.Errorf("reportRunErr(nil) wrote to stderr: %s", stderr.String())
	}
}

// TestReportRunErrCtxCanceled covers the
// `errors.Is(err, context.Canceled) → exitInterrupt` arm.
func TestReportRunErrCtxCanceled(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	if got := reportRunErr(context.Canceled, &stderr); got != exitInterrupt {
		t.Errorf("reportRunErr(ctx.Canceled) = %d, want exitInterrupt", got)
	}
}

// TestReportRunErrGeneric covers the default arm — generic error
// printed to stderr + exitRuntime.
func TestReportRunErrGeneric(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	err := errors.New("synthetic boom")
	if got := reportRunErr(err, &stderr); got != exitRuntime {
		t.Errorf("reportRunErr(boom) = %d, want exitRuntime", got)
	}
	if !strings.Contains(stderr.String(), "synthetic boom") {
		t.Errorf("expected err message in stderr, got %q", stderr.String())
	}
}

// TestSignalContextCancelsViaSigint covers the
// `case <-sigCh: cancel()` arm — send SIGINT to ourselves and
// confirm the returned context fires.
//
// Skipped on Windows where syscall.Kill semantics differ.
func TestSignalContextCancelsViaSigint(t *testing.T) {
	ctx, cancel := signalContext(context.Background())
	defer cancel()

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Skipf("syscall.Kill SIGINT failed: %v", err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Error("ctx not cancelled within 1s after self-SIGINT")
	}
}
