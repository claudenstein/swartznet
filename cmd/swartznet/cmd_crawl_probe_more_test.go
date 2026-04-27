package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/anacrolix/dht/v2/krpc"
)

// TestCmdCrawlProbeBadFlag covers the
// `if err := fs.Parse(args); err != nil { return exitUsage }`
// arm. Unknown flag forces flag.Parse to return ErrHelp.
func TestCmdCrawlProbeBadFlag(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdCrawlProbe([]string{"--no-such-flag"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("bad-flag exit = %d, want exitUsage", code)
	}
}

// TestCmdCrawlProbeBadAddr covers the `net.ResolveUDPAddr err →
// exitUsage` arm. An obviously malformed addr fails resolution
// without leaving the process.
func TestCmdCrawlProbeBadAddr(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdCrawlProbe([]string{"--addr", "totally::not::a::host"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("bad-addr exit = %d, want exitUsage", code)
	}
	if !strings.Contains(stderr.String(), "resolve") {
		t.Errorf("expected 'resolve' in stderr, got %q", stderr.String())
	}
}

// TestCmdCrawlProbeExplicitTargetText covers the
// `else { raw, err := hex.DecodeString(targetHex); ... copy(target[:], raw) }`
// happy-path branch — only the random-target branch is otherwise
// covered. Pair with the responder so the request actually
// completes and the text-output renderer runs.
func TestCmdCrawlProbeExplicitTargetText(t *testing.T) {
	t.Parallel()
	addr := startBep51Responder(t, []krpc.ID{{0x77, 0x88}})

	const target = "0123456789abcdef0123456789abcdef01234567" // 40 hex chars
	var stdout, stderr bytes.Buffer
	code := cmdCrawlProbe([]string{
		"--addr", addr,
		"--target", target,
		"--timeout-ms", "3000",
	}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit = %d (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), target) {
		t.Errorf("expected target %q echoed in output: %s", target, stdout.String())
	}
}

// TestCmdCrawlProbeQueryFails covers the
// `dhtindex.SampleInfohashes err → exitRuntime` arm. Point at a
// loopback port nothing's listening on; a short timeout makes the
// query fail rather than wait forever.
func TestCmdCrawlProbeQueryFails(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	// 127.0.0.1:1 is reserved + nothing listens. timeout 200ms.
	code := cmdCrawlProbe([]string{
		"--addr", "127.0.0.1:1",
		"--timeout-ms", "200",
	}, &stdout, &stderr)
	if code != exitRuntime {
		t.Errorf("query-fail exit = %d, want exitRuntime", code)
	}
}
