package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/swartznet/swartznet/internal/httpapi"
)

// cmdFiles lists a torrent's files or sets one file's priority — a thin
// HTTP client like status.
func cmdFiles(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("files", flag.ContinueOnError)
	fs.SetOutput(stderr)
	apiAddr := fs.String("api-addr", "localhost:7654", "address of the running swartznet HTTP API")
	asJSON := fs.Bool("json", false, "emit JSON instead of a table")
	pos, err := parseFlagsAllowingLeadingPositionals(fs, args)
	if err != nil {
		return exitUsage
	}
	if n := len(pos); n != 1 && n != 3 {
		fmt.Fprintln(stderr, "usage:")
		fmt.Fprintln(stderr, "  swartznet files <infohash>                    # list files")
		fmt.Fprintln(stderr, "  swartznet files <infohash> <index> <priority> # set priority (none|normal|high)")
		return exitUsage
	}
	ih := strings.ToLower(strings.TrimSpace(pos[0]))
	if !validInfoHash(ih) {
		fmt.Fprintln(stderr, "swartznet: infohash must be 40 hex characters")
		return exitUsage
	}

	if len(pos) == 3 {
		idx, err := strconv.Atoi(strings.TrimSpace(pos[1]))
		if err != nil || idx < 0 {
			fmt.Fprintln(stderr, "swartznet: file index must be a non-negative integer")
			return exitUsage
		}
		prio := strings.ToLower(strings.TrimSpace(pos[2]))
		if prio != "none" && prio != "normal" && prio != "high" {
			fmt.Fprintln(stderr, "swartznet: priority must be none/normal/high")
			return exitUsage
		}
		return filesSetPriority(*apiAddr, ih, idx, prio, stdout, stderr)
	}
	return filesList(*apiAddr, ih, *asJSON, stdout, stderr)
}

func filesList(apiAddr, ih string, asJSON bool, stdout, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("http://%s/torrents/%s/files", apiAddr, ih), nil)
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
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(stderr, "swartznet: api status %d: %s\n", resp.StatusCode, body)
		return exitRuntime
	}
	var list httpapi.FilesListResponse
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return reportRunErr(err, stderr)
	}
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(list)
		return exitOK
	}
	if len(list.Files) == 0 {
		fmt.Fprintln(stdout, "(no files — torrent metadata not yet available)")
		return exitOK
	}
	fmt.Fprintf(stdout, "%-4s  %-10s  %8s  %6s  %s\n", "IDX", "PRIORITY", "SIZE", "PROG%", "PATH")
	for _, f := range list.Files {
		fmt.Fprintf(stdout, "%-4d  %-10s  %8s  %5.1f%%  %s\n",
			f.Index, f.Priority, humanBytes(f.Length), f.Progress*100, f.DisplayPath)
	}
	return exitOK
}

func filesSetPriority(apiAddr, ih string, idx int, prio string, stdout, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"priority": prio})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("http://%s/torrents/%s/files/%d/priority", apiAddr, ih, idx),
		bytes.NewReader(body))
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
		respBody, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(stderr, "swartznet: api status %d: %s\n", resp.StatusCode, respBody)
		return exitRuntime
	}
	fmt.Fprintf(stdout, "file %s of %s: priority=%s\n", strconv.Itoa(idx), ih, prio)
	return exitOK
}
