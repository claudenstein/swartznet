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

// cmdConfirm marks a search hit's content as good over the daemon's shared
// confirm path.
func cmdConfirm(args []string, stdout, stderr io.Writer) int {
	return confirmFlag("confirm", args, stdout, stderr)
}

// cmdFlag reports a search hit as bad. It honestly says "no reputations
// changed" when the daemon demoted nobody (the §6 dishonest-success fix).
func cmdFlag(args []string, stdout, stderr io.Writer) int {
	return confirmFlag("flag", args, stdout, stderr)
}

func confirmFlag(action string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	apiAddr := fs.String("api-addr", "localhost:7654", "address of the running swartznet HTTP API")
	pos, err := parseFlagsAllowingLeadingPositionals(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(pos) != 1 {
		fmt.Fprintf(stderr, "usage: swartznet %s <infohash>\n", action)
		return exitUsage
	}
	ih := strings.ToLower(strings.TrimSpace(pos[0]))
	if !validInfoHash(ih) {
		fmt.Fprintln(stderr, "swartznet: infohash must be 40 hex characters")
		return exitUsage
	}

	body, _ := json.Marshal(httpapi.FlagRequest{InfoHash: ih})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+*apiAddr+"/"+action, bytes.NewReader(body))
	if err != nil {
		return reportRunErr(err, stderr)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "swartznet: cannot reach the daemon at %s (%v)\n", *apiAddr, err)
		fmt.Fprintln(stderr, "start it with: swartznet add <magnet>")
		return exitRuntime
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(stderr, "swartznet: api status %d: %s\n", resp.StatusCode, b)
		return exitRuntime
	}
	raw, _ := io.ReadAll(resp.Body)

	if action == "confirm" {
		var out httpapi.ConfirmResponse
		if err := json.Unmarshal(raw, &out); err != nil {
			return reportRunErr(err, stderr)
		}
		fmt.Fprintf(stdout, "confirmed: %s\n", ih)
		if out.IndexersConfirmed > 0 {
			fmt.Fprintf(stdout, "  boosted %d indexer(s)\n", out.IndexersConfirmed)
		}
		return exitOK
	}

	var out httpapi.FlagResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return reportRunErr(err, stderr)
	}
	fmt.Fprintf(stdout, "flagged: %s\n", ih)
	// Honest reporting — never claim a demotion that did not happen.
	switch {
	case out.IndexersFlagged > 0:
		fmt.Fprintf(stdout, "  demoted %d indexer(s)\n", out.IndexersFlagged)
	case out.Attribution == "trusted-exempt":
		fmt.Fprintln(stdout, "  no reputations changed (source is a trusted publisher)")
	case out.Attribution == "trust-unavailable":
		fmt.Fprintln(stdout, "  no reputations changed (trust list unavailable — flag suppressed to protect trusted publishers)")
	default:
		fmt.Fprintln(stdout, "  no reputations changed (this hit has no attributed source)")
	}
	return exitOK
}
