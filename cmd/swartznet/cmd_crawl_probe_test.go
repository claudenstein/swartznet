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

// startBep51Responder runs a one-shot UDP loopback responder
// that serves a BEP-51 sample_infohashes reply with the given
// samples and closes. Mirrors the dhtindex test helper so the
// CLI test stays self-contained.
func startBep51Responder(t *testing.T, samples []krpc.ID) string {
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
	type rPart struct {
		ID       krpc.ID `bencode:"id"`
		Samples  string  `bencode:"samples"`
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

// TestCmdCrawlProbeTextOutput — end-to-end: the CLI queries our
// responder, renders a human-readable summary, and exits 0.
func TestCmdCrawlProbeTextOutput(t *testing.T) {
	t.Parallel()
	sampleA := krpc.ID{0xAA, 0xBB, 0xCC}
	sampleB := krpc.ID{0x11, 0x22, 0x33}
	addr := startBep51Responder(t, []krpc.ID{sampleA, sampleB})

	var stdout, stderr bytes.Buffer
	code := cmdCrawlProbe([]string{"--addr", addr, "--timeout-ms", "3000"}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit = %d (stderr: %s)", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "BEP-51 sample_infohashes probe") {
		t.Errorf("missing header in output: %s", out)
	}
	if !strings.Contains(out, "aabbcc") {
		t.Errorf("missing sampleA hex: %s", out)
	}
	if !strings.Contains(out, "112233") {
		t.Errorf("missing sampleB hex: %s", out)
	}
	if !strings.Contains(out, "num tracked: 42") {
		t.Errorf("missing num field: %s", out)
	}
	if !strings.Contains(out, "interval:   60s") {
		t.Errorf("missing interval: %s", out)
	}
}

// TestCmdCrawlProbeJSONOutput — same roundtrip with --json.
func TestCmdCrawlProbeJSONOutput(t *testing.T) {
	t.Parallel()
	addr := startBep51Responder(t, []krpc.ID{{0xDE, 0xAD, 0xBE, 0xEF}})
	var stdout, stderr bytes.Buffer
	code := cmdCrawlProbe([]string{"--addr", addr, "--json", "--timeout-ms", "3000"}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit = %d (stderr: %s)", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, `"samples": [`) {
		t.Errorf("JSON missing samples array: %s", out)
	}
	if !strings.Contains(out, "deadbeef") {
		t.Errorf("JSON missing sample hex: %s", out)
	}
}

// TestCmdCrawlProbeMissingAddr confirms the required-flag guard.
func TestCmdCrawlProbeMissingAddr(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdCrawlProbe([]string{}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("missing --addr exit = %d, want exitUsage (%d)", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), "--addr is required") {
		t.Errorf("stderr missing --addr hint: %s", stderr.String())
	}
}

// TestCmdCrawlProbeBadTarget rejects a non-40-char target hex.
func TestCmdCrawlProbeBadTarget(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdCrawlProbe([]string{"--addr", "127.0.0.1:1", "--target", "zz"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("bad target exit = %d, want exitUsage", code)
	}
}
