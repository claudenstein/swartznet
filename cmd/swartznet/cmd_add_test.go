package main

import (
	"bytes"
	"testing"
)

// TestCmdAddBadFlag covers cmdAdd's `fs.Parse` err arm.
func TestCmdAddBadFlag(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdAdd([]string{"--no-such-flag"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("bad-flag exit = %d, want exitUsage", code)
	}
}

// TestCmdAddMissingPositional covers `if fs.NArg() != 1` arm.
func TestCmdAddMissingPositional(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdAdd(nil, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("no-args exit = %d, want exitUsage", code)
	}
}
