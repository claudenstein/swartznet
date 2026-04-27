package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestCmdAggregateInspectBadFlag covers cmdAggregateInspect's
// `if err := fs.Parse(args); err != nil { return exitUsage }` arm.
func TestCmdAggregateInspectBadFlag(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdAggregate([]string{"inspect", "--no-such-flag"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("bad-flag exit = %d, want exitUsage", code)
	}
}

// TestCmdAggregateInspectMissingArgs covers the `if fs.NArg() != 1`
// arm.
func TestCmdAggregateInspectMissingArgs(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdAggregate([]string{"inspect"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("no-args exit = %d, want exitUsage", code)
	}
}

// TestCmdAggregateBuildBadHexInIH covers buildAndSign's
// `hex.DecodeString err → record N: decode ih err` arm.
// readRecords accepts any 40-char string in the IH field, so a
// 40-char value with non-hex characters survives that gate and
// fails at hex.DecodeString inside buildAndSign.
func TestCmdAggregateBuildBadHexInIH(t *testing.T) {
	t.Parallel()
	keyPath := filepath.Join(t.TempDir(), "id.key")
	// 40 chars, but 'z' is not hex.
	const badIH = "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"
	body := `{"ih":"` + badIH + `","kw":"alpha","t":1}` + "\n"
	inPath := filepath.Join(t.TempDir(), "input.jsonl")
	if err := os.WriteFile(inPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(t.TempDir(), "out.bin")

	var stdout, stderr bytes.Buffer
	code := cmdAggregate([]string{
		"build",
		"--key", keyPath,
		"--in", inPath,
		"--out", outPath,
	}, &stdout, &stderr)
	if code != exitRuntime {
		t.Errorf("non-hex-ih exit = %d, want exitRuntime", code)
	}
}
