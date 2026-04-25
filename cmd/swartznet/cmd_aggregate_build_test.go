package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTestKey makes a sealed 0600 identity file the CLI can load.
func writeTestKey(t *testing.T) string {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	dir := t.TempDir()
	path := filepath.Join(dir, "identity.key")
	// identity.LoadOrCreate writes the raw ed25519.PrivateKey bytes.
	if err := os.WriteFile(path, priv, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeJSONL writes records in the CLI's input schema.
func writeJSONL(t *testing.T, recs []jsonRecord) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "in.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, r := range recs {
		line := fmt.Sprintf(`{"kw":%q,"ih":%q,"t":%d}`+"\n", r.Kw, r.IH, r.T)
		if _, err := f.WriteString(line); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// End-to-end: build → inspect → find all work against the same file.
func TestCmdAggregateBuildInspectFindChain(t *testing.T) {
	keyPath := writeTestKey(t)

	recs := []jsonRecord{
		{Kw: "linux", IH: hex.EncodeToString(bytes.Repeat([]byte{0x11}, 20)), T: 1},
		{Kw: "ubuntu", IH: hex.EncodeToString(bytes.Repeat([]byte{0x22}, 20)), T: 2},
		{Kw: "ubuntu", IH: hex.EncodeToString(bytes.Repeat([]byte{0x33}, 20)), T: 3},
	}
	inPath := writeJSONL(t, recs)
	outPath := filepath.Join(t.TempDir(), "index.bin")

	// build
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := cmdAggregate([]string{
		"build", "--in", inPath, "--out", outPath, "--key", keyPath, "--seq", "42",
	}, stdout, stderr)
	if code != exitOK {
		t.Fatalf("build exit = %d (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "records:") {
		t.Errorf("build output missing records line: %s", stdout.String())
	}

	// inspect — trailer should report 3 records
	stdout.Reset()
	stderr.Reset()
	code = cmdAggregate([]string{"inspect", outPath}, stdout, stderr)
	if code != exitOK {
		t.Fatalf("inspect exit = %d (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "records:") {
		t.Errorf("inspect missing records line: %s", stdout.String())
	}

	// find ubu → should match both ubuntu records
	stdout.Reset()
	stderr.Reset()
	code = cmdAggregate([]string{"find", outPath, "ubu"}, stdout, stderr)
	if code != exitOK {
		t.Fatalf("find exit = %d (stderr: %s)", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "Matches for prefix \"ubu\": 2") {
		t.Errorf("find should return 2 matches, got: %s", out)
	}
}

func TestCmdAggregateBuildRequiresOut(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := cmdAggregate([]string{"build"}, stdout, stderr)
	if code != exitUsage {
		t.Errorf("build without --out should exitUsage, got %d", code)
	}
}

func TestCmdAggregateBuildRefusesHighPoW(t *testing.T) {
	keyPath := writeTestKey(t)
	outPath := filepath.Join(t.TempDir(), "x.bin")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := cmdAggregate([]string{
		"build", "--out", outPath, "--key", keyPath, "--pow-bits", "50",
	}, stdout, stderr)
	if code != exitUsage {
		t.Errorf("pow-bits=50 should exitUsage, got %d", code)
	}
}

func TestCmdAggregateBuildRejectsBadJSONL(t *testing.T) {
	keyPath := writeTestKey(t)
	badPath := filepath.Join(t.TempDir(), "bad.jsonl")
	if err := os.WriteFile(badPath, []byte("not json at all\n"), 0644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(t.TempDir(), "x.bin")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := cmdAggregate([]string{
		"build", "--in", badPath, "--out", outPath, "--key", keyPath,
	}, stdout, stderr)
	if code == exitOK {
		t.Fatal("bad JSONL should not succeed")
	}
	if !strings.Contains(stderr.String(), "line 1") {
		t.Errorf("expected line number in error: %s", stderr.String())
	}
}

func TestCmdAggregateBuildRejectsBadInfohash(t *testing.T) {
	keyPath := writeTestKey(t)
	recs := []jsonRecord{
		{Kw: "ok", IH: "tooshort", T: 1},
	}
	inPath := writeJSONL(t, recs)
	outPath := filepath.Join(t.TempDir(), "x.bin")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := cmdAggregate([]string{
		"build", "--in", inPath, "--out", outPath, "--key", keyPath,
	}, stdout, stderr)
	if code == exitOK {
		t.Fatal("short ih should fail")
	}
}

func TestCmdAggregateBuildRejectsEmptyInput(t *testing.T) {
	keyPath := writeTestKey(t)
	emptyPath := filepath.Join(t.TempDir(), "empty.jsonl")
	if err := os.WriteFile(emptyPath, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(t.TempDir(), "x.bin")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := cmdAggregate([]string{
		"build", "--in", emptyPath, "--out", outPath, "--key", keyPath,
	}, stdout, stderr)
	if code == exitOK {
		t.Fatal("empty input should fail")
	}
}

// Small PoW (bits=4) still signs and mines quickly enough for tests.
func TestCmdAggregateBuildWithSmallPoW(t *testing.T) {
	keyPath := writeTestKey(t)
	recs := []jsonRecord{
		{Kw: "linux", IH: hex.EncodeToString(bytes.Repeat([]byte{0xAB}, 20)), T: 1},
	}
	inPath := writeJSONL(t, recs)
	outPath := filepath.Join(t.TempDir(), "pow.bin")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := cmdAggregate([]string{
		"build", "--in", inPath, "--out", outPath, "--key", keyPath, "--pow-bits", "4",
	}, stdout, stderr)
	if code != exitOK {
		t.Fatalf("small-PoW build should succeed: %s", stderr.String())
	}

	// Verify via inspect that min_pow_bits survived to the trailer.
	stdout.Reset()
	stderr.Reset()
	code = cmdAggregate([]string{"inspect", outPath}, stdout, stderr)
	if code != exitOK {
		t.Fatal(stderr.String())
	}
	if !strings.Contains(stdout.String(), "min PoW bits:   4") {
		t.Errorf("expected 'min PoW bits:   4' in inspect output, got: %s", stdout.String())
	}
}
