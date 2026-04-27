package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

const validPub = "abababababababababababababababababababababababababababababababab"

func newTrustFile(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "trust.json")
}

// TestCmdTrustNoArgs covers cmdTrust's `if len(args) == 0` arm —
// usage banner + exitUsage.
func TestCmdTrustNoArgs(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdTrust(nil, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("no-args exit = %d, want exitUsage", code)
	}
	if !strings.Contains(stderr.String(), "swartznet trust") {
		t.Errorf("expected usage banner in stderr: %s", stderr.String())
	}
}

// TestCmdTrustUnknownSub covers the `default:` arm of the
// subcommand switch — unknown sub prints usage + exitUsage.
func TestCmdTrustUnknownSub(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdTrust([]string{"weird"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("unknown-sub exit = %d, want exitUsage", code)
	}
	if !strings.Contains(stderr.String(), "unknown subcommand") {
		t.Errorf("expected 'unknown subcommand' in stderr: %s", stderr.String())
	}
}

// TestTrustListDefaultFile covers openTrustStore's empty-path
// arm — when --file is omitted, it falls back to
// config.Default().TrustPath. Override HOME so the test never
// touches the user's real trust list.
func TestTrustListDefaultFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", "")

	var stdout, stderr bytes.Buffer
	code := cmdTrust([]string{"list"}, &stdout, &stderr)
	if code != exitOK {
		t.Errorf("default-file list exit = %d, stderr: %s", code, stderr.String())
	}
}

// TestCmdTrustHelp covers the `case "help"` arm.
func TestCmdTrustHelp(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdTrust([]string{"help"}, &stdout, &stderr)
	if code != exitOK {
		t.Errorf("help exit = %d, want exitOK", code)
	}
	if !strings.Contains(stdout.String(), "swartznet trust") {
		t.Errorf("expected usage banner in stdout: %s", stdout.String())
	}
}

// TestTrustListEmpty covers trustList's `if len(list) == 0` arm.
func TestTrustListEmpty(t *testing.T) {
	t.Parallel()
	file := newTrustFile(t)
	var stdout, stderr bytes.Buffer
	code := cmdTrust([]string{"list", "--file", file}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("empty-list exit = %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "(no trusted publishers") {
		t.Errorf("expected empty-list message: %s", stdout.String())
	}
}

// TestTrustListBadFlag covers trustList's `fs.Parse` err arm.
func TestTrustListBadFlag(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdTrust([]string{"list", "--no-such-flag"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("bad-flag exit = %d, want exitUsage", code)
	}
}

// TestTrustAddAndList covers trustAdd's happy path with a label,
// then trustList rendering with one entry.
func TestTrustAddAndList(t *testing.T) {
	t.Parallel()
	file := newTrustFile(t)

	// Add with label.
	var stdout, stderr bytes.Buffer
	code := cmdTrust([]string{"add", "--file", file, validPub, "team", "ops"}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("add exit = %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "added:") {
		t.Errorf("expected 'added:' in stdout: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "(team ops)") {
		t.Errorf("expected '(team ops)' label in stdout: %s", stdout.String())
	}

	// List with text rendering shows the entry.
	stdout.Reset()
	stderr.Reset()
	code = cmdTrust([]string{"list", "--file", file}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("list exit = %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), validPub) {
		t.Errorf("expected pubkey in list output: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "team ops") {
		t.Errorf("expected label in list output: %s", stdout.String())
	}

	// List as JSON.
	stdout.Reset()
	stderr.Reset()
	code = cmdTrust([]string{"list", "--file", file, "--json"}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("list --json exit = %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"pubkey"`) && !strings.Contains(stdout.String(), `"PubKey"`) {
		t.Errorf("expected pubkey field in JSON list: %s", stdout.String())
	}
}

// TestTrustAddNoArgs covers the `if fs.NArg() < 1` arm.
func TestTrustAddNoArgs(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdTrust([]string{"add"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("no-args add exit = %d, want exitUsage", code)
	}
}

// TestTrustAddBadFlag covers trustAdd's `fs.Parse` err arm.
func TestTrustAddBadFlag(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdTrust([]string{"add", "--no-such-flag"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("bad-flag add exit = %d, want exitUsage", code)
	}
}

// TestTrustAddBadPubkey covers trustAdd's `store.Add err → reportRunErr`
// arm. trust.Store.Add rejects non-64-hex pubkeys.
func TestTrustAddBadPubkey(t *testing.T) {
	t.Parallel()
	file := newTrustFile(t)
	var stdout, stderr bytes.Buffer
	code := cmdTrust([]string{"add", "--file", file, "too-short"}, &stdout, &stderr)
	if code == exitOK {
		t.Errorf("bad-pubkey exit = %d, want non-zero", code)
	}
}

// TestTrustRemoveHappy covers trustRemove's happy path: add then
// remove cleanly.
func TestTrustRemoveHappy(t *testing.T) {
	t.Parallel()
	file := newTrustFile(t)

	var stdout, stderr bytes.Buffer
	code := cmdTrust([]string{"add", "--file", file, validPub}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("setup add exit = %d, stderr: %s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = cmdTrust([]string{"remove", "--file", file, validPub}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("remove exit = %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "removed:") {
		t.Errorf("expected 'removed:' in stdout: %s", stdout.String())
	}
}

// TestTrustRemoveNoArgs covers `if fs.NArg() != 1` in trustRemove.
func TestTrustRemoveNoArgs(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdTrust([]string{"remove"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("no-args remove exit = %d, want exitUsage", code)
	}
}

// TestTrustRemoveBadFlag covers trustRemove's `fs.Parse` err arm.
func TestTrustRemoveBadFlag(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdTrust([]string{"remove", "--no-such-flag"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("bad-flag remove exit = %d, want exitUsage", code)
	}
}

// TestTrustListBadFile covers trustList's `openTrustStore err →
// reportRunErr` arm at lines 68-70. --file points at a directory
// so trust.LoadOrCreate's ReadFile fails with EISDIR.
func TestTrustListBadFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Path is a directory, not a file.
	var stdout, stderr bytes.Buffer
	code := cmdTrust([]string{"list", "--file", dir}, &stdout, &stderr)
	if code == exitOK {
		t.Errorf("bad-file exit = %d, want non-zero", code)
	}
}

// TestTrustAddBadFile covers trustAdd's openTrustStore err arm.
func TestTrustAddBadFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := cmdTrust([]string{"add", "--file", dir, validPub}, &stdout, &stderr)
	if code == exitOK {
		t.Errorf("bad-file add exit = %d, want non-zero", code)
	}
}

// TestTrustRemoveBadFile covers trustRemove's openTrustStore err
// arm.
func TestTrustRemoveBadFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := cmdTrust([]string{"remove", "--file", dir, validPub}, &stdout, &stderr)
	if code == exitOK {
		t.Errorf("bad-file remove exit = %d, want non-zero", code)
	}
}

// trust.Store.Remove is idempotent + only errors on save (e.g.
// disk failure), so trustRemove's `store.Remove err → reportRunErr`
// arm is hard to trigger portably; left uncovered.
