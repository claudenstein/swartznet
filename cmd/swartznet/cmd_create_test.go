package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCmdCreateBadFlag covers cmdCreate's `fs.Parse` err arm.
func TestCmdCreateBadFlag(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdCreate([]string{"--no-such-flag"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("bad-flag exit = %d, want exitUsage", code)
	}
}

// TestCmdCreateMissingPositional covers the `if fs.NArg() != 1`
// arm.
func TestCmdCreateMissingPositional(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdCreate([]string{"-o", "/tmp/x.torrent"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("missing-arg exit = %d, want exitUsage", code)
	}
}

// TestCmdCreateMissingOutput covers the `if out == "" → exitUsage`
// arm: positional supplied but no -o.
func TestCmdCreateMissingOutput(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdCreate([]string{"/tmp/whatever"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("missing-out exit = %d, want exitUsage", code)
	}
	if !strings.Contains(stderr.String(), "-o <output.torrent>") {
		t.Errorf("expected -o hint in stderr, got %q", stderr.String())
	}
}

// TestCmdCreateHappyPath covers the success path: write a tiny
// file, hash it via the in-process engine, verify the output
// .torrent file is produced and stdout shows the InfoHash line.
func TestCmdCreateHappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	if err := os.WriteFile(src, []byte("hello swartznet"), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "out.torrent")

	var stdout, stderr bytes.Buffer
	code := cmdCreate([]string{"-o", outPath, src}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("happy exit = %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "InfoHash:") {
		t.Errorf("expected 'InfoHash:' in stdout: %s", stdout.String())
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Errorf("expected %s to exist: %v", outPath, err)
	}
}

// TestCmdCreateBadIdentityPath covers the
// `if sign { … if err != nil { return reportRunErr ... } }` arm.
// Plant a directory at the identity path so LoadOrCreate fails.
func TestCmdCreateBadIdentityPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "out.torrent")

	// Plant a directory at the identity path.
	idPath := filepath.Join(dir, "id.key")
	if err := os.Mkdir(idPath, 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := cmdCreate([]string{
		"-o", outPath,
		"--sign",
		"--identity", idPath,
		src,
	}, &stdout, &stderr)
	if code == exitOK {
		t.Errorf("bad-identity exit = %d, want non-zero", code)
	}
}

// TestStringSliceFlag covers the stringSliceFlag.String + Set
// methods used by --tracker / --webseed.
func TestStringSliceFlag(t *testing.T) {
	t.Parallel()
	var s stringSliceFlag
	if got := s.String(); got != "" {
		t.Errorf("empty String() = %q, want \"\"", got)
	}
	if err := s.Set("a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("b"); err != nil {
		t.Fatal(err)
	}
	if got := s.String(); got != "a,b" {
		t.Errorf("String() after two Sets = %q, want %q", got, "a,b")
	}
	if len(s) != 2 || s[0] != "a" || s[1] != "b" {
		t.Errorf("slice contents = %v, want [a b]", []string(s))
	}
}
