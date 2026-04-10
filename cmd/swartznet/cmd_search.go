package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/indexer"
)

// cmdSearch implements `swartznet search <query>`.
//
// M2.0 queries the torrent-level Bleve index. M2.2 will extend this to also
// match against file contents once the extractor pipeline is in place.
func cmdSearch(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		indexDir string
		limit    int
		asJSON   bool
	)
	fs.StringVar(&indexDir, "index-dir", "", "path to the Bleve index (default: ~/.local/share/swartznet/index)")
	fs.IntVar(&limit, "limit", 20, "maximum results to return")
	fs.BoolVar(&asJSON, "json", false, "emit JSON instead of text")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(stderr, "usage: swartznet search [--limit N] [--json] <query...>")
		return exitUsage
	}
	query := strings.Join(fs.Args(), " ")

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
