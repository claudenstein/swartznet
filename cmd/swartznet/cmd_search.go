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

// cmdSearch searches Layer L. It runs against the daemon's HTTP API when any
// networked layer (--swarm/--dht) or the --signed-by filter is requested;
// otherwise it opens the Bleve index directly (works with no daemon running).
func cmdSearch(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(stderr)
	indexDir := fs.String("index-dir", "", "path to the Bleve index (default: ~/.local/share/swartznet/index)")
	limit := fs.Int("limit", 20, "max results")
	asJSON := fs.Bool("json", false, "emit JSON instead of text")
	signedBy := fs.String("signed-by", "", "restrict local results to torrents signed by this 64-char hex pubkey")
	apiAddr := fs.String("api-addr", "localhost:7654", "address of the running swartznet HTTP API")
	swarm := fs.Bool("swarm", false, "also query connected peers (Layer S)")
	dht := fs.Bool("dht", false, "also query the DHT keyword index (Layer D)")
	swarmTimeout := fs.Int("swarm-timeout-ms", 2000, "Layer-S timeout")
	dhtTimeout := fs.Int("dht-timeout-ms", 5000, "Layer-D timeout")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(stderr, "usage: swartznet search [--limit N] [--json] [--swarm] [--dht] <query...>")
		return exitUsage
	}
	query := strings.Join(fs.Args(), " ")

	// --signed-by/--swarm/--dht require the shared JSON shape, so they route
	// through the daemon API.
	if *swarm || *dht || *signedBy != "" {
		return searchViaAPI(*apiAddr, query, *limit, *signedBy, *swarm, *dht, *swarmTimeout, *dhtTimeout, *asJSON, stdout, stderr)
	}
	return searchDirect(*indexDir, query, *limit, *asJSON, stdout, stderr)
}

func searchDirect(indexDir, query string, limit int, asJSON bool, stdout, stderr io.Writer) int {
	cfg := config.Default()
	if indexDir != "" {
		cfg.IndexDir = indexDir
	}
	// Bleve's on-disk index is single-writer: a running daemon holds the
	// lock and a direct open would block forever. Bound the open and fail
	// closed with a route hint rather than hang.
	idx, err := openIndexWithTimeout(cfg.IndexDir, 3*time.Second)
	if err != nil {
		if err == errIndexOpenTimeout {
			fmt.Fprintln(stderr, "swartznet: the local index is locked (a daemon is likely running)")
			fmt.Fprintln(stderr, "route the search through the daemon: swartznet search --swarm <query>")
			return exitRuntime
		}
		return reportRunErr(err, stderr)
	}
	defer idx.Close()

	resp, err := idx.Search(indexer.SearchRequest{Query: query, Limit: limit, Highlight: true})
	if err != nil {
		return reportRunErr(err, stderr)
	}
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(resp)
		return exitOK
	}
	emitSearchText(stdout, query, resp)
	return exitOK
}

var errIndexOpenTimeout = fmt.Errorf("index open timed out")

// openIndexWithTimeout opens the Bleve index, giving up after d (the index
// is single-writer, so a running daemon holding the lock would otherwise
// block forever). A late successful open is closed so it can't leak.
func openIndexWithTimeout(dir string, d time.Duration) (*indexer.Index, error) {
	type result struct {
		idx *indexer.Index
		err error
	}
	ch := make(chan result, 1)
	go func() {
		idx, err := indexer.Open(dir)
		ch <- result{idx, err}
	}()
	select {
	case r := <-ch:
		return r.idx, r.err
	case <-time.After(d):
		go func() {
			if r := <-ch; r.idx != nil {
				_ = r.idx.Close()
			}
		}()
		return nil, errIndexOpenTimeout
	}
}

func emitSearchText(w io.Writer, query string, resp *indexer.SearchResponse) {
	fmt.Fprintf(w, "Query: %s\n", query)
	fmt.Fprintf(w, "Total: %d hits  (returning %d, took %s)\n", resp.Total, len(resp.Hits), resp.Took)
	if len(resp.Hits) == 0 {
		fmt.Fprintln(w, "(no results — try `swartznet add <magnet>` to build up the local index)")
		return
	}
	for i, h := range resp.Hits {
		if h.DocType == "content" {
			fmt.Fprintf(w, "%3d. [content] score=%.3f  mime=%s  extractor=%s\n", i+1, h.Score, h.Mime, h.Extractor)
			fmt.Fprintf(w, "     %s  (%s)\n", h.FilePath, humanBytes(h.FileSize))
			fmt.Fprintf(w, "     in torrent: %s\n", h.InfoHash)
		} else {
			fmt.Fprintf(w, "%3d. [torrent] [%s] score=%.3f  files=%d  size=%s\n", i+1, h.InfoHash, h.Score, h.FileCount, humanBytes(h.SizeBytes))
			fmt.Fprintf(w, "     %s\n", h.Name)
			if len(h.Trackers) > 0 {
				line := "     tracker: " + h.Trackers[0]
				if more := len(h.Trackers) - 1; more > 0 {
					line += fmt.Sprintf(" (+%d more)", more)
				}
				fmt.Fprintln(w, line)
			}
		}
		if snip := firstFragment(h.Fragments); snip != "" {
			fmt.Fprintf(w, "     … %s\n", stripMarks(snip))
		}
	}
}

// firstFragment returns the first available highlighted fragment, preferring
// the content body.
func firstFragment(frags map[string][]string) string {
	for _, field := range []string{"text", "name", "files"} {
		if f := frags[field]; len(f) > 0 {
			return f[0]
		}
	}
	return ""
}

// stripMarks removes the <mark> wrappers for plain-text rendering (Bleve
// pre-escapes the text, so the result is safe to print).
func stripMarks(s string) string {
	s = strings.ReplaceAll(s, "<mark>", "")
	return strings.ReplaceAll(s, "</mark>", "")
}

func searchViaAPI(apiAddr, query string, limit int, signedBy string, swarm, dht bool, swarmTimeout, dhtTimeout int, asJSON bool, stdout, stderr io.Writer) int {
	body, _ := json.Marshal(httpapi.SearchRequestBody{
		Q: query, Limit: limit, SignedBy: signedBy,
		Swarm: swarm, DHT: dht,
		SwarmTimeout: swarmTimeout, DHTTimeout: dhtTimeout,
		Highlight: true,
	})
	timeout := time.Duration(swarmTimeout+dhtTimeout+2000) * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+apiAddr+"/search", bytes.NewReader(body))
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
	var out httpapi.SearchResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return reportRunErr(err, stderr)
	}
	fmt.Fprintf(stdout, "Query: %s\n", query)
	fmt.Fprintf(stdout, "Local: %d hits\n\n", out.Local.Total)
	if len(out.Local.Hits) == 0 {
		fmt.Fprintln(stdout, "(no results)")
		return exitOK
	}
	fmt.Fprintln(stdout, "=== LOCAL ===")
	for i, h := range out.Local.Hits {
		if h.DocType == "content" {
			fmt.Fprintf(stdout, "%3d. [content] %s  (%s)  extractor=%s\n", i+1, h.FilePath, h.Mime, h.Extractor)
			fmt.Fprintf(stdout, "     infohash: %s  score=%.3f\n", h.InfoHash, h.Score)
		} else {
			fmt.Fprintf(stdout, "%3d. [torrent] %s\n", i+1, h.Name)
			fmt.Fprintf(stdout, "     infohash: %s  size=%s  score=%.3f\n", h.InfoHash, humanBytes(h.SizeBytes), h.Score)
		}
		if snip := firstFragment(h.Fragments); snip != "" {
			fmt.Fprintf(stdout, "     … %s\n", stripMarks(snip))
		}
	}
	return exitOK
}
