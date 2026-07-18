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

// cmdIndex either reports index stats (no args) or toggles per-torrent
// indexing (index <infohash> on|off).
func cmdIndex(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("index", flag.ContinueOnError)
	fs.SetOutput(stderr)
	apiAddr := fs.String("api-addr", "localhost:7654", "address of the running swartznet HTTP API")
	asJSON := fs.Bool("json", false, "emit JSON instead of text")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	switch fs.NArg() {
	case 0:
		return indexStats(*apiAddr, *asJSON, stdout, stderr)
	case 2:
		return indexToggle(*apiAddr, fs.Arg(0), fs.Arg(1), stdout, stderr)
	default:
		fmt.Fprintln(stderr, "usage: swartznet index                    # show index stats")
		fmt.Fprintln(stderr, "       swartznet index <infohash> on|off  # toggle per-torrent indexing")
		return exitUsage
	}
}

func indexStats(apiAddr string, asJSON bool, stdout, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+apiAddr+"/index/stats", nil)
	if err != nil {
		return reportRunErr(err, stderr)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "swartznet: cannot reach the daemon at %s (%v)\n", apiAddr, err)
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
	if asJSON {
		var pretty bytes.Buffer
		if json.Indent(&pretty, raw, "", "  ") == nil {
			stdout.Write(pretty.Bytes())
			fmt.Fprintln(stdout)
		} else {
			stdout.Write(raw)
		}
		return exitOK
	}
	var st httpapi.IndexStats
	if err := json.Unmarshal(raw, &st); err != nil {
		return reportRunErr(err, stderr)
	}
	fmt.Fprintln(stdout, "Local index (Layer L):")
	fmt.Fprintf(stdout, "  documents:      %d  (%d torrents, %d content chunks)\n", st.DocCount, st.TorrentCount, st.ContentCount)
	fmt.Fprintf(stdout, "  index size:     %s\n", humanBytes(st.DirBytes))
	fmt.Fprintf(stdout, "  corpus text:    %s\n", humanBytes(st.CorpusTextBytes))
	fmt.Fprintf(stdout, "  inflation:      %.2fx\n", st.InflationRatio)
	return exitOK
}

func indexToggle(apiAddr, ihArg, modeArg string, stdout, stderr io.Writer) int {
	ih := strings.ToLower(strings.TrimSpace(ihArg))
	if !validInfoHash(ih) {
		fmt.Fprintln(stderr, "swartznet: infohash must be 40 hex characters")
		return exitUsage
	}
	var enabled bool
	switch strings.ToLower(strings.TrimSpace(modeArg)) {
	case "on", "true", "1", "yes":
		enabled = true
	case "off", "false", "0", "no":
		enabled = false
	default:
		fmt.Fprintln(stderr, "swartznet: mode must be 'on' or 'off'")
		return exitUsage
	}

	body, _ := json.Marshal(map[string]bool{"enabled": enabled})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("http://%s/torrents/%s/indexing", apiAddr, ih), bytes.NewReader(body))
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
		b, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(stderr, "swartznet: api status %d: %s\n", resp.StatusCode, b)
		return exitRuntime
	}
	if enabled {
		fmt.Fprintf(stdout, "indexing on: %s\n", ih)
	} else {
		fmt.Fprintf(stdout, "indexing off: %s\n", ih)
	}
	return exitOK
}
