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

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/httpapi"
	"github.com/swartznet/swartznet/internal/indexer"
)

// cmdSearch implements `swartznet search <query>`.
//
// Two modes:
//
//   - Default (no --swarm): opens the Bleve index directly and runs a
//     local-only search. Works without a running daemon but only sees
//     torrents already indexed on disk.
//   - --swarm: POSTs to the running `swartznet add` daemon's HTTP API
//     (see internal/httpapi), which runs the same local search AND a
//     distributed sn_search fan-out across connected peers.
func cmdSearch(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		indexDir    string
		limit       int
		asJSON      bool
		useSwarm    bool
		apiAddr     string
		swarmTimeMs int
	)
	fs.StringVar(&indexDir, "index-dir", "", "path to the Bleve index (default: ~/.local/share/swartznet/index)")
	fs.IntVar(&limit, "limit", 20, "maximum results to return")
	fs.BoolVar(&asJSON, "json", false, "emit JSON instead of text")
	fs.BoolVar(&useSwarm, "swarm", false, "also query search-capable peers (requires a running `swartznet add` daemon)")
	fs.StringVar(&apiAddr, "api-addr", "localhost:7654", "address of the running swartznet HTTP API")
	fs.IntVar(&swarmTimeMs, "swarm-timeout-ms", 2000, "swarm fan-out timeout in milliseconds")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(stderr, "usage: swartznet search [--limit N] [--json] [--swarm] <query...>")
		return exitUsage
	}
	query := strings.Join(fs.Args(), " ")

	if useSwarm {
		return cmdSearchViaAPI(stdout, stderr, apiAddr, query, limit, swarmTimeMs, asJSON)
	}

	// Direct local-only path: open the Bleve index in-process.
	cfg := config.Default()
	if indexDir != "" {
		cfg.IndexDir = indexDir
	}

	idx, err := indexer.Open(cfg.IndexDir)
	if err != nil {
		return reportRunErr(err, stderr)
	}
	defer idx.Close()

	res, err := idx.Search(indexer.SearchRequest{Query: query, Limit: limit})
	if err != nil {
		return reportRunErr(err, stderr)
	}

	if asJSON {
		return emitJSON(stdout, res, stderr)
	}
	return emitText(stdout, res, query)
}

// cmdSearchViaAPI talks to a running `swartznet add` daemon over the
// local HTTP API to run a combined local + swarm search.
func cmdSearchViaAPI(stdout, stderr io.Writer, apiAddr, query string, limit, swarmTimeoutMs int, asJSON bool) int {
	body, err := json.Marshal(httpapi.SearchRequest{
		Q:              query,
		Limit:          limit,
		Swarm:          true,
		SwarmTimeoutMs: swarmTimeoutMs,
	})
	if err != nil {
		return reportRunErr(err, stderr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(swarmTimeoutMs+2000)*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", "http://"+apiAddr+"/search", bytes.NewReader(body))
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

	var apiResp httpapi.SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return reportRunErr(err, stderr)
	}

	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(apiResp); err != nil {
			return reportRunErr(err, stderr)
		}
		return exitOK
	}
	return emitSwarmText(stdout, &apiResp, query)
}

// emitSwarmText prints a combined local + swarm result set in the
// human-readable text format.
func emitSwarmText(w io.Writer, res *httpapi.SearchResponse, query string) int {
	fmt.Fprintf(w, "Query: %s\n", query)
	fmt.Fprintf(w, "Local: %d hits\n", res.Local.Total)
	if res.Swarm != nil {
		fmt.Fprintf(w, "Swarm: asked=%d, responded=%d, rejected=%d, hits=%d\n",
			res.Swarm.Asked, res.Swarm.Responded, res.Swarm.Rejected, len(res.Swarm.Hits))
		if res.Swarm.Error != "" {
			fmt.Fprintf(w, "Swarm error: %s\n", res.Swarm.Error)
		}
	}
	fmt.Fprintln(w)

	if len(res.Local.Hits) == 0 && (res.Swarm == nil || len(res.Swarm.Hits) == 0) {
		fmt.Fprintln(w, "(no results)")
		return exitOK
	}

	if len(res.Local.Hits) > 0 {
		fmt.Fprintln(w, "=== LOCAL ===")
		for i, h := range res.Local.Hits {
			printLocalHit(w, i+1, h)
		}
	}
	if res.Swarm != nil && len(res.Swarm.Hits) > 0 {
		fmt.Fprintln(w, "=== SWARM ===")
		for i, h := range res.Swarm.Hits {
			printSwarmHit(w, i+1, h)
		}
	}
	return exitOK
}

func printLocalHit(w io.Writer, n int, h httpapi.LocalHit) {
	switch h.DocType {
	case "content":
		fmt.Fprintf(w, "%3d. [content] %s  (%s)  extractor=%s\n",
			n, h.FilePath, humanBytes(h.SizeBytes), h.Extractor)
		fmt.Fprintf(w, "     infohash: %s  score=%.3f\n", h.InfoHash, h.Score)
	default:
		fmt.Fprintf(w, "%3d. [torrent] %s\n", n, h.Name)
		fmt.Fprintf(w, "     infohash: %s  size=%s  score=%.3f\n",
			h.InfoHash, humanBytes(h.SizeBytes), h.Score)
	}
	fmt.Fprintln(w)
}

func printSwarmHit(w io.Writer, n int, h httpapi.SwarmHit) {
	fmt.Fprintf(w, "%3d. %s\n", n, h.Name)
	fmt.Fprintf(w, "     infohash: %s  size=%s  seeders=%d  score=%d  sources=%d\n",
		h.InfoHash, humanBytes(h.Size), h.Seeders, h.Score, len(h.Sources))
	fmt.Fprintln(w)
}

func emitJSON(w io.Writer, res *indexer.SearchResponse, errW io.Writer) int {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		return reportRunErr(err, errW)
	}
	return exitOK
}

func emitText(w io.Writer, res *indexer.SearchResponse, query string) int {
	fmt.Fprintf(w, "Query: %s\n", query)
	fmt.Fprintf(w, "Total: %d hits  (returning %d, took %s)\n\n", res.Total, len(res.Hits), res.Took)
	if len(res.Hits) == 0 {
		fmt.Fprintln(w, "(no results — try `swartznet add <magnet>` to build up the local index)")
		return exitOK
	}
	for i, h := range res.Hits {
		switch h.DocType {
		case "content":
			fmt.Fprintf(w, "%3d. [content] score=%.3f  mime=%s  extractor=%s\n",
				i+1, h.Score, h.Mime, h.Extractor)
			fmt.Fprintf(w, "     %s  (%s)\n", h.FilePath, humanBytes(h.FileSize))
			fmt.Fprintf(w, "     in torrent: %s\n", h.InfoHash)
		default: // "torrent" or anything unexpected
			fmt.Fprintf(w, "%3d. [torrent] [%s] score=%.3f  files=%d  size=%s\n",
				i+1, h.InfoHash, h.Score, h.FileCount, humanBytes(h.SizeBytes))
			fmt.Fprintf(w, "     %s\n", h.Name)
			if len(h.Trackers) > 0 {
				preview := h.Trackers[0]
				if len(h.Trackers) > 1 {
					preview = fmt.Sprintf("%s (+%d more)", preview, len(h.Trackers)-1)
				}
				fmt.Fprintf(w, "     tracker: %s\n", preview)
			}
		}
		fmt.Fprintln(w)
	}
	return exitOK
}
