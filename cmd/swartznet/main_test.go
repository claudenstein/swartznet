package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestRunDispatch(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		wantCode   int
		wantOut    string // substring of stdout
		wantErrOut string // substring of stderr
	}{
		{"no args", nil, exitUsage, "", "Usage:"},
		{"help", []string{"help"}, exitOK, "Usage:", ""},
		{"-h", []string{"-h"}, exitOK, "Usage:", ""},
		{"--help", []string{"--help"}, exitOK, "Usage:", ""},
		{"version", []string{"version"}, exitOK, "swartznet " + Version + "\n", ""},
		{"-v", []string{"-v"}, exitOK, "swartznet " + Version + "\n", ""},
		{"--version", []string{"--version"}, exitOK, "swartznet " + Version + "\n", ""},
		{"unknown", []string{"wat"}, exitUsage, "", "swartznet: unknown command \"wat\"\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tc.args, &stdout, &stderr)
			if code != tc.wantCode {
				t.Fatalf("exit = %d, want %d (stderr: %s)", code, tc.wantCode, stderr.String())
			}
			if tc.wantOut != "" && !strings.Contains(stdout.String(), tc.wantOut) {
				t.Fatalf("stdout %q lacks %q", stdout.String(), tc.wantOut)
			}
			if tc.wantErrOut != "" && !strings.Contains(stderr.String(), tc.wantErrOut) {
				t.Fatalf("stderr %q lacks %q", stderr.String(), tc.wantErrOut)
			}
		})
	}
}

// TestUsageDocumentsEnvVars pins the §6 fix: the env knobs must be
// user-discoverable from the built-in help.
func TestUsageDocumentsEnvVars(t *testing.T) {
	var out bytes.Buffer
	printUsage(&out)
	for _, want := range []string{"SWARTZNET_LOG", "SWARTZNET_UNSAFE", "debug|info|warn|error"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("usage text lacks %q", want)
		}
	}
}

func TestUnknownFlagIsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"status", "--definitely-not-a-flag"}, &stdout, &stderr); code != exitUsage {
		t.Fatalf("exit = %d, want %d", code, exitUsage)
	}
}

func TestNewLoggerLevels(t *testing.T) {
	for _, tc := range []struct {
		env      string
		debugOn  bool
		infoOn   bool
		warnsRaw bool // expect the unrecognized-value warning
	}{
		{"", false, true, false},
		{"info", false, true, false},
		{"debug", true, true, false},
		{"warn", false, false, false},
		{"error", false, false, false},
		{"verbose", false, true, true},
		{"DEBUG", false, true, true}, // values are not case-folded
	} {
		t.Run("SWARTZNET_LOG="+tc.env, func(t *testing.T) {
			t.Setenv("SWARTZNET_LOG", tc.env)
			var buf bytes.Buffer
			log := newLogger(&buf)
			ctx := context.Background()
			if got := log.Enabled(ctx, slog.LevelDebug); got != tc.debugOn {
				t.Errorf("debug enabled = %v, want %v", got, tc.debugOn)
			}
			if got := log.Enabled(ctx, slog.LevelInfo); got != tc.infoOn {
				t.Errorf("info enabled = %v, want %v", got, tc.infoOn)
			}
			hasWarn := strings.Contains(buf.String(), "unrecognized SWARTZNET_LOG value")
			if hasWarn != tc.warnsRaw {
				t.Errorf("warning present = %v, want %v (buf: %s)", hasWarn, tc.warnsRaw, buf.String())
			}
		})
	}
}

func TestReportRunErr(t *testing.T) {
	var stderr bytes.Buffer
	if code := reportRunErr(nil, &stderr); code != exitOK {
		t.Fatalf("nil → %d", code)
	}
	if code := reportRunErr(context.Canceled, &stderr); code != exitInterrupt {
		t.Fatalf("Canceled → %d, want 130", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("clean interrupt must print nothing, got %q", stderr.String())
	}
	if code := reportRunErr(errTest, &stderr); code != exitRuntime {
		t.Fatalf("error → %d, want 1", code)
	}
	if got := stderr.String(); got != "swartznet: boom\n" {
		t.Fatalf("stderr = %q", got)
	}
}

var errTest = errBoom{}

type errBoom struct{}

func (errBoom) Error() string { return "boom" }
