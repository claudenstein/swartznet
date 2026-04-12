package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// cmdIndex implements `swartznet index <infohash> on|off`. Flips
// the per-torrent indexing toggle on a running daemon via
// POST /torrents/{infohash}/indexing.
func cmdIndex(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("index", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var apiAddr string
	fs.StringVar(&apiAddr, "api-addr", "localhost:7654", "address of the running swartznet HTTP API")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 2 {
		fmt.Fprintln(stderr, "usage: swartznet index <infohash> on|off")
		return exitUsage
	}
	ih := strings.ToLower(strings.TrimSpace(fs.Arg(0)))
	mode := strings.ToLower(strings.TrimSpace(fs.Arg(1)))
	if len(ih) != 40 {
		fmt.Fprintln(stderr, "swartznet: infohash must be 40 hex characters")
		return exitUsage
	}
	var enabled bool
	switch mode {
	case "on", "true", "1", "yes":
		enabled = true
	case "off", "false", "0", "no":
		enabled = false
	default:
		fmt.Fprintln(stderr, "swartznet: mode must be 'on' or 'off'")
		return exitUsage
	}

	body, err := json.Marshal(map[string]any{"enabled": enabled})
	if err != nil {
		return reportRunErr(err, stderr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "http://" + apiAddr + "/torrents/" + ih + "/indexing"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return reportRunErr(err, stderr)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "swartznet: cannot reach the daemon at %s (%v)\n", apiAddr, err)
		return exitRuntime
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(stderr, "swartznet: api status %d: %s\n", resp.StatusCode, data)
		return exitRuntime
	}
	state := "on"
	if !enabled {
		state = "off"
	}
	fmt.Fprintf(stdout, "indexing %s: %s\n", state, ih)
	return exitOK
}
