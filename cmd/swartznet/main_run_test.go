package main

import (
	"bytes"
	"context"
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
