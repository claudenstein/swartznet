package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRecs writes a JSONL record file and returns its path.
func writeRecs(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "recs.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAggregateBuildInspectFindRoundTrip(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir()) // build auto-creates the identity here
	recs := writeRecs(t,
		`{"kw":"ubuntu","ih":"0000000000000000000000000000000000000001","t":1700000000}`,
		`{"kw":"ubuntu","ih":"0000000000000000000000000000000000000002","t":1700000001}`,
		`{"kw":"debian","ih":"0000000000000000000000000000000000000003","t":1700000002}`,
	)
	out := filepath.Join(t.TempDir(), "idx.snagg")

	var so, se bytes.Buffer
	if code := cmdAggregate([]string{"build", "--in", recs, "--out", out, "--seq", "7"}, &so, &se); code != exitOK {
		t.Fatalf("build exit=%d stderr=%s", code, se.String())
	}
	if !strings.Contains(so.String(), "records:     3") || !strings.Contains(so.String(), "fingerprint:") {
		t.Errorf("build output = %s", so.String())
	}
	// Output file must be mode 0644.
	if fi, err := os.Stat(out); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm() != 0o644 {
		t.Errorf("output mode = %o, want 644", fi.Mode().Perm())
	}

	so.Reset()
	se.Reset()
	if code := cmdAggregate([]string{"inspect", out}, &so, &se); code != exitOK {
		t.Fatalf("inspect exit=%d stderr=%s", code, se.String())
	}
	if !strings.Contains(so.String(), "records:        3") || !strings.Contains(so.String(), "sequence:       7") {
		t.Errorf("inspect output = %s", so.String())
	}

	so.Reset()
	se.Reset()
	if code := cmdAggregate([]string{"find", "--verify", out, "ubu"}, &so, &se); code != exitOK {
		t.Fatalf("find exit=%d stderr=%s", code, se.String())
	}
	if !strings.Contains(so.String(), "2 records") {
		t.Errorf("find(ubu) output = %s", so.String())
	}
}

func TestAggregateBuildRefusesHighPoW(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	recs := writeRecs(t, `{"kw":"x","ih":"0000000000000000000000000000000000000001"}`)
	var so, se bytes.Buffer
	code := cmdAggregate([]string{"build", "--in", recs, "--out", filepath.Join(t.TempDir(), "o"), "--pow-bits", "41"}, &so, &se)
	if code != exitUsage {
		t.Fatalf("pow-bits 41 exit=%d, want %d", code, exitUsage)
	}
	if !strings.Contains(se.String(), "above 40 refused") {
		t.Errorf("stderr = %s", se.String())
	}
}

func TestAggregateBuildRequiresOut(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	var so, se bytes.Buffer
	if code := cmdAggregate([]string{"build", "--in", "-"}, &so, &se); code != exitUsage {
		t.Errorf("missing --out exit=%d, want %d", code, exitUsage)
	}
}

func TestAggregateBuildRejectsBadRecords(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	out := filepath.Join(t.TempDir(), "o")
	// Short ih.
	bad := writeRecs(t, `{"kw":"x","ih":"deadbeef"}`)
	var so, se bytes.Buffer
	if code := cmdAggregate([]string{"build", "--in", bad, "--out", out}, &so, &se); code != exitRuntime {
		t.Errorf("short ih exit=%d, want %d", code, exitRuntime)
	}
	if !strings.Contains(se.String(), "want 40") {
		t.Errorf("stderr = %s", se.String())
	}
	// Empty kw.
	se.Reset()
	bad2 := writeRecs(t, `{"kw":"","ih":"0000000000000000000000000000000000000001"}`)
	if code := cmdAggregate([]string{"build", "--in", bad2, "--out", out}, &so, &se); code != exitRuntime {
		t.Errorf("empty kw exit=%d, want %d", code, exitRuntime)
	}
}

func TestAggregateInspectFailsOnGarbage(t *testing.T) {
	t.Parallel()
	garbage := filepath.Join(t.TempDir(), "garbage.snagg")
	os.WriteFile(garbage, bytes.Repeat([]byte{0x00}, 16384*3), 0o644)
	var so, se bytes.Buffer
	if code := cmdAggregate([]string{"inspect", garbage}, &so, &se); code != exitRuntime {
		t.Errorf("inspect garbage exit=%d, want %d", code, exitRuntime)
	}
	if !strings.Contains(se.String(), "open b-tree") {
		t.Errorf("stderr = %s", se.String())
	}
}

func TestAggregateFindVerifyFailsOnTamperedFingerprint(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	recs := writeRecs(t, `{"kw":"ubuntu","ih":"0000000000000000000000000000000000000001"}`)
	out := filepath.Join(t.TempDir(), "idx.snagg")
	var so, se bytes.Buffer
	if code := cmdAggregate([]string{"build", "--in", recs, "--out", out}, &so, &se); code != exitOK {
		t.Fatalf("build failed: %s", se.String())
	}
	// Tamper a leaf record byte (breaks the fingerprint but not the trailer sig).
	data, _ := os.ReadFile(out)
	// leaf is piece 1 (root=0, leaf=1, trailer=2); flip a payload byte deep in it.
	data[16384+64] ^= 0xFF
	os.WriteFile(out, data, 0o644)

	so.Reset()
	se.Reset()
	// Without --verify, Find may still succeed or drop the record; WITH --verify
	// the fingerprint mismatch must fail.
	code := cmdAggregate([]string{"find", "--verify", out, "ubu"}, &so, &se)
	if code != exitRuntime {
		t.Fatalf("find --verify on tampered index exit=%d, want %d (stderr=%s)", code, exitRuntime, se.String())
	}
	if !strings.Contains(se.String(), "fingerprint verification failed") {
		t.Errorf("stderr = %s", se.String())
	}
}
