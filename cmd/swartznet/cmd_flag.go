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

	"github.com/swartznet/swartznet/internal/httpapi"
)

// cmdFlag implements `swartznet flag <infohash>`. POSTs to the
// running daemon's /flag endpoint, which decrements every known
// indexer's reputation for the given infohash. Used by the user
// to mark a hit as spam or unwanted.
func cmdFlag(args []string, stdout, stderr io.Writer) int {
	return cmdFlagOrConfirm("flag", "/flag", args, stdout, stderr)
}

// cmdConfirm implements `swartznet confirm <infohash>`. POSTs to
// /confirm, which adds the infohash to the known-good Bloom
// filter so future Layer-D queries boost it.
//
// Auto-confirm on download completion is wired in the engine, so
// most users will never need this command — it exists for the
// "this came from elsewhere but I trust it" case.
func cmdConfirm(args []string, stdout, stderr io.Writer) int {
	return cmdFlagOrConfirm("confirm", "/confirm", args, stdout, stderr)
}

func cmdFlagOrConfirm(name, endpoint string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var apiAddr string
	fs.StringVar(&apiAddr, "api-addr", "localhost:7654", "address of the running swartznet HTTP API")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintf(stderr, "usage: swartznet %s <infohash>\n", name)
		return exitUsage
	}
	infoHash := strings.ToLower(strings.TrimSpace(fs.Arg(0)))
	if len(infoHash) != 40 {
		fmt.Fprintln(stderr, "swartznet: infohash must be 40 hex characters")
		return exitUsage
	}

	body, err := json.Marshal(httpapi.FlagRequest{InfoHash: infoHash})
	if err != nil {
		return reportRunErr(err, stderr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", "http://"+apiAddr+endpoint, bytes.NewReader(body))
	if err != nil {
		return reportRunErr(err, stderr)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "swartznet: cannot reach the daemon at %s (%v)\n", apiAddr, err)
		fmt.Fprintln(stderr, "start it with: swartznet add <magnet>")
		return exitRuntime
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(stderr, "swartznet: api status %d: %s\n", resp.StatusCode, data)
		return exitRuntime
	}

	var out httpapi.FlagResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return reportRunErr(err, stderr)
	}
	if out.OK {
		// English-friendly past tense: "flag" → "flagged",
		// "confirm" → "confirmed". Avoid the lazy "%sed" format
		// which yields "flaged".
		past := name + "ed"
		if name == "flag" {
			past = "flagged"
		}
		fmt.Fprintf(stdout, "%s: %s\n", past, out.InfoHash)
		return exitOK
	}
	fmt.Fprintf(stderr, "swartznet: %s did not succeed\n", name)
	return exitRuntime
}
