package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/krpc"
	"github.com/swartznet/swartznet/internal/dhtindex"
)

// cmdCrawlProbe implements `swartznet crawl-probe` — a
// diagnostic one-shot that issues a single BEP-51
// sample_infohashes query against a DHT address and prints the
// response. Pure ops tooling: no running daemon needed, no state
// touched. Useful for validating that a node supports BEP-51
// and for hand-inspecting the samples it volunteers during
// Channel-B crawler development.
//
// The node ID target defaults to a fresh random 20-byte string
// each invocation so the sampled "slice" of the address space
// varies between runs.
func cmdCrawlProbe(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("crawl-probe", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		addrStr   string
		targetHex string
		timeoutMs int
		asJSON    bool
	)
	fs.StringVar(&addrStr, "addr", "", "DHT node address to probe (host:port, required)")
	fs.StringVar(&targetHex, "target", "", "20-byte hex target (default: random)")
	fs.IntVar(&timeoutMs, "timeout-ms", 5000, "query timeout in milliseconds")
	fs.BoolVar(&asJSON, "json", false, "emit JSON instead of human text")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if addrStr == "" {
		fmt.Fprintln(stderr, "swartznet crawl-probe: --addr is required")
		return exitUsage
	}

	udp, err := net.ResolveUDPAddr("udp", addrStr)
	if err != nil {
		fmt.Fprintf(stderr, "swartznet crawl-probe: resolve %q: %v\n", addrStr, err)
		return exitUsage
	}

	var target krpc.ID
	if targetHex == "" {
		if _, err := rand.Read(target[:]); err != nil {
			return reportRunErr(err, stderr)
		}
	} else {
		raw, err := hex.DecodeString(targetHex)
		if err != nil || len(raw) != 20 {
			fmt.Fprintf(stderr, "swartznet crawl-probe: --target must be 40 hex chars (20 bytes), got %q\n", targetHex)
			return exitUsage
		}
		copy(target[:], raw)
	}

	// Spin up a local loopback DHT server just long enough to
	// issue the query. NoSecurity so we can use an arbitrary
	// node ID; Passive so we don't respond to incoming queries.
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		return reportRunErr(fmt.Errorf("bind loopback: %w", err), stderr)
	}
	defer conn.Close()
	srv, err := dht.NewServer(&dht.ServerConfig{
		Conn:       conn,
		NoSecurity: true,
		Passive:    true,
	})
	if err != nil {
		return reportRunErr(fmt.Errorf("dht.NewServer: %w", err), stderr)
	}
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	res, err := dhtindex.SampleInfohashes(ctx, srv, dht.NewAddr(udp), target)
	if err != nil {
		fmt.Fprintf(stderr, "swartznet crawl-probe: query failed: %v\n", err)
		return exitRuntime
	}

	if asJSON {
		// krpc.ID doesn't marshal cleanly to JSON — render
		// samples + nodes as plain hex strings.
		out := struct {
			Addr     string   `json:"addr"`
			Target   string   `json:"target"`
			Samples  []string `json:"samples"`
			Interval int64    `json:"interval"`
			Num      int64    `json:"num"`
			Nodes    []string `json:"nodes"`
		}{
			Addr:     addrStr,
			Target:   hex.EncodeToString(target[:]),
			Interval: res.Interval,
			Num:      res.Num,
		}
		for _, s := range res.Samples {
			out.Samples = append(out.Samples, hex.EncodeToString(s[:]))
		}
		for _, n := range res.Nodes {
			out.Nodes = append(out.Nodes, fmt.Sprintf("%s@%s", hex.EncodeToString(n.ID[:]), n.Addr.String()))
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return reportRunErr(err, stderr)
		}
		return exitOK
	}

	fmt.Fprintf(stdout, "BEP-51 sample_infohashes probe\n")
	fmt.Fprintf(stdout, "  peer:       %s\n", addrStr)
	fmt.Fprintf(stdout, "  target:     %s\n", hex.EncodeToString(target[:]))
	fmt.Fprintf(stdout, "  interval:   %ds\n", res.Interval)
	fmt.Fprintf(stdout, "  num tracked: %d\n", res.Num)
	fmt.Fprintf(stdout, "  samples (%d):\n", len(res.Samples))
	for _, s := range res.Samples {
		fmt.Fprintf(stdout, "    %s\n", hex.EncodeToString(s[:]))
	}
	fmt.Fprintf(stdout, "  closest nodes (%d):\n", len(res.Nodes))
	for _, n := range res.Nodes {
		fmt.Fprintf(stdout, "    %s @ %s\n", hex.EncodeToString(n.ID[:]), n.Addr.String())
	}
	return exitOK
}
