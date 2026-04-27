package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestCmdAggregateFindBadFlag covers cmdAggregateFind's
// `if err := fs.Parse(args); err != nil { return exitUsage }` arm.
// An unknown flag makes flag.Parse return ErrHelp.
func TestCmdAggregateFindBadFlag(t *testing.T) {
	t.Parallel()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := cmdAggregate([]string{"find", "--no-such-flag", "x.bin", "p"}, stdout, stderr)
	if code != exitUsage {
		t.Errorf("bad-flag exit = %d, want exitUsage", code)
	}
}

// TestCmdAggregateFindMissingArgs covers the
// `if fs.NArg() != 2` usage arm.
func TestCmdAggregateFindMissingArgs(t *testing.T) {
	t.Parallel()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := cmdAggregate([]string{"find"}, stdout, stderr)
	if code != exitUsage {
		t.Errorf("missing-args exit = %d, want exitUsage", code)
	}
}

// TestCmdAggregateFindReadFileError covers the
// `os.ReadFile err → exitRuntime` arm: pass a path that doesn't
// exist.
func TestCmdAggregateFindReadFileError(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "nope.bin")
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := cmdAggregate([]string{"find", missing, "prefix"}, stdout, stderr)
	if code != exitRuntime {
		t.Errorf("missing-file exit = %d, want exitRuntime", code)
	}
}

// TestCmdAggregateFindOpenBTreeError covers the
// `companion.OpenBTree err → exitRuntime` arm: hand it a real
// file with garbage bytes so the b-tree header check fails.
func TestCmdAggregateFindOpenBTreeError(t *testing.T) {
	t.Parallel()
	bogus := filepath.Join(t.TempDir(), "bogus.bin")
	if err := os.WriteFile(bogus, []byte("this is not a btree page"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := cmdAggregate([]string{"find", bogus, "prefix"}, stdout, stderr)
	if code != exitRuntime {
		t.Errorf("garbage-file exit = %d, want exitRuntime", code)
	}
}

// TestCmdAggregateFindReaderFindErr covers cmdAggregateFind's
// `hits, err := reader.Find(prefix); if err != nil` arm at
// cmd_aggregate.go:146-150. Build a valid tree, then corrupt a
// leaf page byte so DecodeLeaf rejects when Find walks to it.
// OpenBTree only validates the trailer, so it passes; the err
// only surfaces in reader.Find. Skip --verify so VerifyFingerprint
// (which would also error) doesn't short-circuit first.
func TestCmdAggregateFindReaderFindErr(t *testing.T) {
	t.Parallel()
	path, _ := buildTestIndexFile(t)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const minPieceSize = 16384
	if len(body) < 2*minPieceSize {
		t.Fatalf("test index too small: %d bytes", len(body))
	}
	// Wreck the leaf page beyond repair: zero its 16-byte header
	// so decodeHeader (called from DecodeLeaf) rejects the
	// version/kind. The trailer, root header, and root payload
	// are intact, so OpenBTree + walkToLeaves still succeed and
	// the err only fires inside Find's per-leaf DecodeLeaf call.
	for i := 0; i < 16; i++ {
		body[minPieceSize+i] = 0
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	// No --verify, so VerifyFingerprint doesn't run; the leaf
	// corruption only matters when Find walks to it.
	code := cmdAggregate([]string{"find", path, "linux"}, stdout, stderr)
	if code != exitRuntime {
		t.Errorf("find-err exit = %d, want exitRuntime; stderr: %s", code, stderr.String())
	}
}

// TestCmdAggregateFindVerifyFails covers the
// `reader.VerifyFingerprint err → exitRuntime` arm. Build a valid
// b-tree, flip a byte in the middle to corrupt a leaf page so
// the recomputed fingerprint disagrees with the trailer's
// signed value.
func TestCmdAggregateFindVerifyFails(t *testing.T) {
	t.Parallel()
	path, _ := buildTestIndexFile(t)
	// Read, corrupt one byte in the leaf area (well past the
	// header), write back.
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) < 100 {
		t.Fatalf("test index too small: %d bytes", len(body))
	}
	// 5 records build to root[0] + leaf[1] + trailer[2] each
	// 16384 bytes. Flip a byte well inside the leaf page's
	// payload (offset 16384 + 30 sits in record bytes, past the
	// 16-byte page header and 2-byte record count).
	const minPieceSize = 16384
	body[minPieceSize+30] ^= 0xFF
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := cmdAggregate([]string{"find", "--verify", path, "linux"}, stdout, stderr)
	if code != exitRuntime {
		t.Errorf("verify-fail exit = %d, want exitRuntime; stderr: %s", code, stderr.String())
	}
}
