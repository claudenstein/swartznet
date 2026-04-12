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

// cmdFiles implements `swartznet files <infohash> [<index> <priority>]`.
// Without extra args: lists every file in the torrent with
// priority and progress. With index+priority: flips a single
// file's priority via POST /torrents/{ih}/files/{index}/priority.
func cmdFiles(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("files", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		apiAddr string
		asJSON  bool
	)
	fs.StringVar(&apiAddr, "api-addr", "localhost:7654", "address of the running swartznet HTTP API")
	fs.BoolVar(&asJSON, "json", false, "emit JSON instead of a table")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	switch fs.NArg() {
	case 1:
		return filesList(apiAddr, fs.Arg(0), asJSON, stdout, stderr)
	case 3:
		return filesSetPriority(apiAddr, fs.Arg(0), fs.Arg(1), fs.Arg(2), stdout, stderr)
	default:
		fmt.Fprintln(stderr, "usage:")
		fmt.Fprintln(stderr, "  swartznet files <infohash>                    # list files")
		fmt.Fprintln(stderr, "  swartznet files <infohash> <index> <priority> # set priority (none|normal|high)")
		return exitUsage
	}
}

func filesList(apiAddr, ihRaw string, asJSON bool, stdout, stderr io.Writer) int {
	ih := strings.ToLower(strings.TrimSpace(ihRaw))
	if len(ih) != 40 {
		fmt.Fprintln(stderr, "swartznet: infohash must be 40 hex characters")
		return exitUsage
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "http://" + apiAddr + "/torrents/" + ih + "/files"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return reportRunErr(err, stderr)
	}
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

	var body httpapi.FilesListResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return reportRunErr(err, stderr)
	}

	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(body); err != nil {
			return reportRunErr(err, stderr)
		}
		return exitOK
	}

	if len(body.Files) == 0 {
		fmt.Fprintln(stdout, "(no files — torrent metadata not yet available)")
		return exitOK
	}

	fmt.Fprintf(stdout, "%-4s  %-10s  %8s  %6s  %s\n", "IDX", "PRIORITY", "SIZE", "PROG%", "PATH")
	for _, f := range body.Files {
		fmt.Fprintf(stdout, "%-4d  %-10s  %8s  %5.1f%%  %s\n",
			f.Index, f.Priority, humanBytes(f.Length), f.Progress*100, f.DisplayPath)
	}
	return exitOK
}

func filesSetPriority(apiAddr, ihRaw, idxRaw, priority string, stdout, stderr io.Writer) int {
	ih := strings.ToLower(strings.TrimSpace(ihRaw))
	if len(ih) != 40 {
		fmt.Fprintln(stderr, "swartznet: infohash must be 40 hex characters")
		return exitUsage
	}
	idx := strings.TrimSpace(idxRaw)
	prio := strings.ToLower(strings.TrimSpace(priority))
	switch prio {
	case "none", "normal", "high":
		// ok
	default:
		fmt.Fprintln(stderr, "swartznet: priority must be none/normal/high")
		return exitUsage
	}

	body, err := json.Marshal(map[string]any{"priority": prio})
	if err != nil {
		return reportRunErr(err, stderr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "http://" + apiAddr + "/torrents/" + ih + "/files/" + idx + "/priority"
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
	fmt.Fprintf(stdout, "file %s of %s: priority=%s\n", idx, ih, prio)
	return exitOK
}
