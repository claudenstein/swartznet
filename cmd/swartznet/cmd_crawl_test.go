package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anacrolix/dht/v2/krpc"
)

// TestCrawlReachesNodeAndDiscovers — end-to-end over a loopback BEP-51 responder:
// the crawl samples the seed, discovers its infohashes, and exits 0.
func TestCrawlReachesNodeAndDiscovers(t *testing.T) {
	t.Parallel()
	addr := startBep51Responder(t, []krpc.ID{{0xAA, 0xBB, 0xCC}, {0x11, 0x22, 0x33}})
	var stdout, stderr bytes.Buffer
	code := cmdCrawl([]string{"--seed", addr, "--timeout-ms", "3000", "--duration-ms", "4000"}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit = %d (stderr: %s)", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"aabbcc", "112233"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing infohash %q:\n%s", want, out)
		}
	}
	if !strings.Contains(stderr.String(), "1 nodes sampled") {
		t.Errorf("stderr missing sample count:\n%s", stderr.String())
	}
}

// TestCrawlJSON — same round-trip with --json (structured DoD gate).
func TestCrawlJSON(t *testing.T) {
	t.Parallel()
	addr := startBep51Responder(t, []krpc.ID{{0xDE, 0xAD, 0xBE, 0xEF}})
	var stdout, stderr bytes.Buffer
	code := cmdCrawl([]string{"--seed", addr, "--json", "--timeout-ms", "3000", "--duration-ms", "4000"}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit = %d (stderr: %s)", code, stderr.String())
	}
	var got struct {
		Seeds          int      `json:"seeds"`
		NodesSampled   int      `json:"nodes_sampled"`
		InfohashesSeen int      `json:"infohashes_seen"`
		Infohashes     []string `json:"infohashes"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, stdout.String())
	}
	if got.NodesSampled != 1 || got.InfohashesSeen != 1 || len(got.Infohashes) != 1 {
		t.Errorf("stats = %+v, want 1 node / 1 infohash", got)
	}
	if len(got.Infohashes) == 1 && !strings.HasPrefix(got.Infohashes[0], "deadbeef") {
		t.Errorf("infohash = %q, want deadbeef prefix", got.Infohashes[0])
	}
}

// TestCrawlUnreachableExitsNonZero — the fail-on-all-fail contract: a crawl that
// reaches no node exits 1 (not a silent success).
func TestCrawlUnreachableExitsNonZero(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdCrawl([]string{"--seed", "127.0.0.1:1", "--timeout-ms", "250", "--duration-ms", "1500"}, &stdout, &stderr)
	if code != exitRuntime {
		t.Fatalf("unreachable crawl exit = %d, want %d (stderr: %s)", code, exitRuntime, stderr.String())
	}
	if !strings.Contains(stderr.String(), "reached no DHT nodes") {
		t.Errorf("stderr missing failure hint:\n%s", stderr.String())
	}
}

// TestCrawlWorkersGuard rejects a non-positive worker count.
func TestCrawlWorkersGuard(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdCrawl([]string{"--workers", "0"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("--workers 0 exit = %d, want %d", code, exitUsage)
	}
}

// TestResolveCrawlSeeds covers dedup + malformed-seed counting (pure, hermetic).
func TestResolveCrawlSeeds(t *testing.T) {
	t.Parallel()
	nodes, errs := resolveCrawlSeeds([]string{
		"127.0.0.1:6881",
		"127.0.0.1:6881", // duplicate → collapsed
		"not-a-host:port",
		"127.0.0.1:0", // zero port → rejected
	})
	if len(nodes) != 1 {
		t.Errorf("resolved %d nodes, want 1 (dedup)", len(nodes))
	}
	if errs != 2 {
		t.Errorf("errs = %d, want 2 (bad host + zero port)", errs)
	}
}
