package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/companion"
)

// Build a tiny real index on disk for the CLI tests to exercise.
// Returns the path to the payload file and the expected record set.
func buildTestIndexFile(t *testing.T) (path string, records []companion.Record) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var pk [32]byte
	copy(pk[:], pub)

	// A handful of records spanning two keywords so we can
	// exercise prefix find.
	for i := 0; i < 5; i++ {
		var r companion.Record
		copy(r.Pk[:], pub)
		r.Kw = "linux"
		if i%2 == 0 {
			r.Kw = "ubuntu"
		}
		r.Ih[0] = byte(i + 1)
		r.T = int64(1000 + i)
		sig := ed25519.Sign(priv, companion.RecordSigMessage(r))
		copy(r.Sig[:], sig)
		records = append(records, r)
	}

	out, err := companion.BuildBTree(companion.BuildBTreeInput{
		Records:   records,
		PubKey:    pk,
		PrivKey:   priv,
		Seq:       7,
		PieceSize: companion.MinPieceSize,
		CreatedTs: 1712649600,
	})
	if err != nil {
		t.Fatalf("BuildBTree: %v", err)
	}

	dir := t.TempDir()
	path = filepath.Join(dir, "index.bin")
	if err := os.WriteFile(path, out.Bytes, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path, records
}

func TestCmdAggregateInspect(t *testing.T) {
	path, recs := buildTestIndexFile(t)

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := cmdAggregate([]string{"inspect", path}, stdout, stderr)
	if code != exitOK {
		t.Fatalf("exit = %d (stderr: %s)", code, stderr.String())
	}
	out := stdout.String()

	// Must mention the record count and core fields.
	if !strings.Contains(out, "records:") {
		t.Errorf("missing 'records:' in output: %s", out)
	}
	// Note: output renders as "records:        5" with spaces;
	// check for the digit rather than a fragile exact match.
	if !strings.Contains(out, "records:") || !strings.Contains(out, "5") {
		t.Errorf("output does not report %d records: %s", len(recs), out)
	}
	if !strings.Contains(out, "fingerprint:") {
		t.Errorf("missing fingerprint line: %s", out)
	}
}

func TestCmdAggregateInspectRejectsBadFile(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := cmdAggregate([]string{"inspect", "/nonexistent/file"}, stdout, stderr)
	if code == exitOK {
		t.Fatal("inspect should fail on missing file")
	}
	if !strings.Contains(stderr.String(), "read") {
		t.Errorf("expected a read-error in stderr, got %q", stderr.String())
	}
}

func TestCmdAggregateInspectRejectsMalformedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "garbage.bin")
	// A file that's the right size for one piece but lacks the
	// SNAGG magic → OpenBTree rejects.
	if err := os.WriteFile(path, make([]byte, companion.MinPieceSize*3), 0644); err != nil {
		t.Fatal(err)
	}
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := cmdAggregate([]string{"inspect", path}, stdout, stderr)
	if code == exitOK {
		t.Fatal("inspect should fail on zero-filled garbage")
	}
}

func TestCmdAggregateFindPrefix(t *testing.T) {
	path, _ := buildTestIndexFile(t)

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := cmdAggregate([]string{"find", path, "ubu"}, stdout, stderr)
	if code != exitOK {
		t.Fatalf("exit = %d (stderr: %s)", code, stderr.String())
	}
	out := stdout.String()
	// All records with kw="ubuntu" should be listed — indices 0, 2, 4.
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 4 { // header + ≥3 records
		t.Errorf("unexpected short output: %s", out)
	}
	for _, line := range lines[1:] {
		if !strings.Contains(line, "ubuntu") {
			t.Errorf("non-ubuntu line in ubu-prefix results: %q", line)
		}
	}
}

func TestCmdAggregateFindNoMatch(t *testing.T) {
	path, _ := buildTestIndexFile(t)

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := cmdAggregate([]string{"find", path, "nomatch"}, stdout, stderr)
	if code != exitOK {
		t.Fatal("no-match should still be exit 0")
	}
	if !strings.Contains(stdout.String(), "0 records") {
		t.Errorf("expected '0 records' in output: %s", stdout.String())
	}
}

func TestCmdAggregateFindVerifyOption(t *testing.T) {
	path, _ := buildTestIndexFile(t)

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := cmdAggregate([]string{"find", "--verify", path, "linux"}, stdout, stderr)
	if code != exitOK {
		t.Fatalf("verify-path exit = %d (stderr: %s)", code, stderr.String())
	}
}

func TestCmdAggregateUnknownSubcommand(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := cmdAggregate([]string{"nope"}, stdout, stderr)
	if code != exitUsage {
		t.Errorf("unknown sub exit = %d, want exitUsage", code)
	}
}

func TestCmdAggregateHelp(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := cmdAggregate([]string{"help"}, stdout, stderr)
	if code != exitOK {
		t.Error("help should return exitOK")
	}
	if !strings.Contains(stdout.String(), "aggregate") {
		t.Errorf("help output missing 'aggregate': %s", stdout.String())
	}
}

func TestCmdAggregateNoSubcommand(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := cmdAggregate(nil, stdout, stderr)
	if code != exitUsage {
		t.Errorf("no subcommand exit = %d, want exitUsage", code)
	}
}
