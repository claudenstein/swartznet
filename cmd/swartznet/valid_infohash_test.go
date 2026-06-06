package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestValidInfoHash is a table-driven unit test for the shared
// infohash validator: exactly 40 lowercase-hex chars are accepted;
// wrong length and 40-char-but-non-hex inputs are rejected.
func TestValidInfoHash(t *testing.T) {
	t.Parallel()
	const ok = "0123456789abcdef0123456789abcdef01234567" // 40 hex
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"valid hex", ok, true},
		{"empty", "", false},
		{"too short", "abc", false},
		{"too long", ok + "0", false},
		// Right length, but not hex — the case the old length-only
		// check let slip through to the daemon.
		{"40 non-hex", strings.Repeat("z", 40), false},
		{"40 with one non-hex", "g" + ok[1:], false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validInfoHash(tc.in); got != tc.want {
				t.Errorf("validInfoHash(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestCmdInfohashCommandsRejectNonHex confirms each infohash-taking
// command rejects a 40-char non-hex argument locally (exitUsage)
// rather than forwarding it to the daemon. Covers flag/index/files
// (both list and set-priority paths).
func TestCmdInfohashCommandsRejectNonHex(t *testing.T) {
	t.Parallel()
	const nonHex = "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz" // 40 non-hex
	type runner struct {
		name string
		fn   func(args []string, stdout, stderr *bytes.Buffer) int
		args []string
	}
	runners := []runner{
		{"flag", func(a []string, o, e *bytes.Buffer) int { return cmdFlag(a, o, e) }, []string{nonHex}},
		{"index", func(a []string, o, e *bytes.Buffer) int { return cmdIndex(a, o, e) }, []string{nonHex, "on"}},
		{"files-list", func(a []string, o, e *bytes.Buffer) int { return cmdFiles(a, o, e) }, []string{nonHex}},
		{"files-set", func(a []string, o, e *bytes.Buffer) int { return cmdFiles(a, o, e) }, []string{nonHex, "0", "high"}},
	}
	for _, r := range runners {
		t.Run(r.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := r.fn(r.args, &stdout, &stderr)
			if code != exitUsage {
				t.Errorf("%s non-hex exit = %d, want exitUsage", r.name, code)
			}
			if !strings.Contains(stderr.String(), "40 hex characters") {
				t.Errorf("%s stderr = %q, want it to mention '40 hex characters'", r.name, stderr.String())
			}
		})
	}
}
