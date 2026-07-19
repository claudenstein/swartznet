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
// otherwise it opens the Bleve index directly (works with no daemon running),
// and if that index is locked by a running daemon it transparently falls back
// to a local-only search routed through that daemon — so a plain `search
// <query>` works in both cases.
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
		return parseErrExit(err)
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
	return searchDirect(*apiAddr, *indexDir, query, *limit, *swarmTimeout, *dhtTimeout, *asJSON, stdout, stderr)
}

func searchDirect(apiAddr, indexDir, query string, limit, swarmTimeout, dhtTimeout int, asJSON bool, stdout, stderr io.Writer) int {
	cfg := config.Default()
	if indexDir != "" {
		cfg.IndexDir = indexDir
	}
	// Bleve's on-disk index is single-writer: a running daemon holds the lock
	// and a direct open would block forever. Bound the open; if it times out a
	// daemon is holding the index, so fall back to a LOCAL-only search routed
	// through that daemon — this way `search <query>` works whether or not a
	// daemon is running, instead of failing in the common (daemon-up) case.
	idx, err := openIndexWithTimeout(cfg.IndexDir, 3*time.Second)
	if err != nil {
		if err == errIndexOpenTimeout {
			return searchViaAPI(apiAddr, query, limit, "", false, false, swarmTimeout, dhtTimeout, asJSON, stdout, stderr)
		}
		return reportRunErr(err, stderr)
	}
	defer idx.Close()

	resp, err := idx.Search(indexer.SearchRequest{Query: query, Limit: limit, Highlight: true})
	if err != nil {
		return reportRunErr(err, stderr)
	}
	if asJSON {
		// Emit the SAME `{"local":{...}}` envelope the daemon-routed path emits,
		// so `search --json` yields ONE stable schema whether or not a daemon is
		// running (the lock-fallback routes through searchViaAPI, which dumps the
		// httpapi shape; a bare direct dump of indexer.SearchResponse would be a
		// different, incompatible schema for the identical invocation).
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(httpapi.SearchResponse{Local: toAPILocalBlock(*resp)})
		return exitOK
	}
	emitSearchText(stdout, query, resp)
	return exitOK
}

// toAPILocalBlock converts a direct indexer result into the httpapi local block,
// so the CLI's direct and daemon-routed --json outputs share one schema.
func toAPILocalBlock(r indexer.SearchResponse) httpapi.LocalBlock {
	hits := make([]httpapi.LocalHit, 0, len(r.Hits))
	for _, h := range r.Hits {
		hits = append(hits, httpapi.LocalHit{
			DocType:   h.DocType,
			InfoHash:  h.InfoHash,
			Name:      h.Name,
			SizeBytes: h.SizeBytes,
			FileIndex: h.FileIndex,
			FilePath:  h.FilePath,
			Mime:      h.Mime,
			Extractor: h.Extractor,
			Score:     h.Score,
			SignedBy:  h.SignedBy,
			Fragments: h.Fragments,
		})
	}
	return httpapi.LocalBlock{Total: r.Total, Hits: hits}
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
	if len(out.Local.Hits) > 0 {
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
	}
	if out.Swarm != nil {
		fmt.Fprintf(stdout, "\n=== SWARM (Layer S) === asked=%d responded=%d\n", out.Swarm.Asked, out.Swarm.Responded)
		if out.Swarm.Error != "" {
			fmt.Fprintf(stdout, "     (error: %s)\n", out.Swarm.Error)
		}
		for i, h := range out.Swarm.Hits {
			fmt.Fprintf(stdout, "%3d. %s  %s  seeders=%d  sources=%d\n", i+1, h.InfoHash, h.Name, h.Seeders, len(h.Sources))
		}
	}
	if out.Dht != nil {
		fmt.Fprintf(stdout, "\n=== DHT (Layer D) === indexers=%d/%d\n", out.Dht.IndexersResponded, out.Dht.IndexersAsked)
		if out.Dht.Error != "" {
			fmt.Fprintf(stdout, "     (error: %s)\n", out.Dht.Error)
		}
		for i, h := range out.Dht.Hits {
			fmt.Fprintf(stdout, "%3d. %s  %s  seeders=%d  score=%.3f  sources=%d\n", i+1, h.InfoHash, h.Name, h.Seeders, h.Score, len(h.Sources))
		}
	}
	if len(out.Local.Hits) == 0 && out.Swarm == nil && out.Dht == nil {
		fmt.Fprintln(stdout, "(no results)")
	}
	return exitOK
}
