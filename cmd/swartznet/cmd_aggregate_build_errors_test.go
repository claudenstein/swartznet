package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestCmdAggregateBuildBadFlag covers cmdAggregateBuild's
// `if err := fs.Parse(args); err != nil { return exitUsage }` arm.
func TestCmdAggregateBuildBadFlag(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdAggregate([]string{"build", "--no-such-flag"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("bad-flag exit = %d, want exitUsage", code)
	}
}

// TestCmdAggregateBuildLoadPrivKeyError covers the
// `priv, pub, err := loadPrivKey(...); if err != nil →
// exitRuntime` arm. Plant a directory at --key so LoadOrCreate
// fails on read.
func TestCmdAggregateBuildLoadPrivKeyError(t *testing.T) {
	t.Parallel()
	keyDir := filepath.Join(t.TempDir(), "id.key")
	if err := os.Mkdir(keyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(t.TempDir(), "out.bin")

	var stdout, stderr bytes.Buffer
	code := cmdAggregate([]string{
		"build",
		"--key", keyDir,
		"--out", outPath,
	}, &stdout, &stderr)
	if code != exitRuntime {
		t.Errorf("load-key-fail exit = %d, want exitRuntime", code)
	}
}

// TestCmdAggregateBuildWriteFileError covers the
// `os.WriteFile err → exitRuntime` arm: --out points at a path
// inside a non-existent directory.
func TestCmdAggregateBuildWriteFileError(t *testing.T) {
	t.Parallel()
	keyPath := filepath.Join(t.TempDir(), "id.key")
	const ih = "0123456789abcdef0123456789abcdef01234567"
	body := `{"ih":"` + ih + `","kw":"alpha","t":1}` + "\n"
	inPath := filepath.Join(t.TempDir(), "input.jsonl")
	if err := os.WriteFile(inPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	// outPath in a subdir that does not exist.
	outPath := filepath.Join(t.TempDir(), "no-such-dir", "out.bin")

	var stdout, stderr bytes.Buffer
	code := cmdAggregate([]string{
		"build",
		"--key", keyPath,
		"--in", inPath,
		"--out", outPath,
	}, &stdout, &stderr)
	if code != exitRuntime {
		t.Errorf("write-fail exit = %d, want exitRuntime", code)
	}
}
