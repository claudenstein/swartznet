package main

import (
	"bytes"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/dht/v2/krpc"
	"github.com/anacrolix/torrent/bencode"
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

// startBep51ResponderWithNodes is a variant of startBep51Responder
// that also includes a `nodes` field in the reply (one packed
// 26-byte node entry: 20-byte ID + 4-byte IP + 2-byte port).
func startBep51ResponderWithNodes(t *testing.T, samples []krpc.ID) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	buf := make([]byte, 0, 20*len(samples))
	for _, s := range samples {
		buf = append(buf, s[:]...)
	}
	// Build a single 26-byte node entry: ID(20) + IPv4(4) + port(2).
	nodeBuf := make([]byte, 26)
	nodeBuf[0] = 0xCA
	nodeBuf[1] = 0xFE
	nodeBuf[20] = 127
	nodeBuf[21] = 0
	nodeBuf[22] = 0
	nodeBuf[23] = 1
	nodeBuf[24] = 0x1A
	nodeBuf[25] = 0xE1

	type rPart struct {
		ID       krpc.ID `bencode:"id"`
		Samples  string  `bencode:"samples"`
		Nodes    string  `bencode:"nodes"`
		Interval int64   `bencode:"interval"`
		Num      int64   `bencode:"num"`
	}
	type customReply struct {
		T string `bencode:"t"`
		Y string `bencode:"y"`
		R rPart  `bencode:"r"`
	}

	go func() {
		rbuf := make([]byte, 2048)
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, from, err := conn.ReadFrom(rbuf)
		if err != nil {
			return
		}
		var q krpc.Msg
		if err := bencode.Unmarshal(rbuf[:n], &q); err != nil {
			return
		}
		reply := customReply{
			T: q.T,
			Y: "r",
			R: rPart{
				ID:       krpc.ID{0xCA, 0xFE},
				Samples:  string(buf),
				Nodes:    string(nodeBuf),
				Interval: 60,
				Num:      42,
			},
		}
		out, err := bencode.Marshal(reply)
		if err != nil {
			return
		}
		_, _ = conn.WriteTo(out, from)
	}()
	return conn.LocalAddr().String()
}

// TestCmdCrawlProbeWithNodesText covers the text-output node loop
// at lines 138-140. The responder includes one packed node entry
// so res.Nodes has length 1, exercising the for-each render.
func TestCmdCrawlProbeWithNodesText(t *testing.T) {
	t.Parallel()
	addr := startBep51ResponderWithNodes(t, []krpc.ID{{0x99}})
	var stdout, stderr bytes.Buffer
	code := cmdCrawlProbe([]string{"--addr", addr, "--timeout-ms", "3000"}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit = %d (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "127.0.0.1") {
		t.Errorf("expected node IP 127.0.0.1 in output: %s", stdout.String())
	}
}

// TestCmdCrawlProbeWithNodesJSON covers the JSON-output node loop
// at lines 117-119.
func TestCmdCrawlProbeWithNodesJSON(t *testing.T) {
	t.Parallel()
	addr := startBep51ResponderWithNodes(t, []krpc.ID{{0x99}})
	var stdout, stderr bytes.Buffer
	code := cmdCrawlProbe([]string{"--addr", addr, "--json", "--timeout-ms", "3000"}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit = %d (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "127.0.0.1") {
		t.Errorf("expected node IP in JSON: %s", stdout.String())
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
