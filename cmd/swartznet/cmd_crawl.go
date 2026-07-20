package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/krpc"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// defaultCrawlSeeds are the standard mainline DHT bootstrap routers. They rarely
// answer sample_infohashes themselves, but they return neighbour nodes that seed
// the crawl frontier.
var defaultCrawlSeeds = []string{
	"router.bittorrent.com:6881",
	"router.utorrent.com:6881",
	"dht.transmissionbt.com:6881",
	"dht.libtorrent.org:25401",
}

// cmdCrawl is `swartznet crawl` — a bounded BEP-51 crawl of the mainline DHT that
// samples infohashes from the nodes it reaches and expands its frontier from
// their neighbours, printing the unique infohashes it discovers. Pure ops
// tooling over the deterministic dhtindex.Crawler: no running daemon, no state
// touched, no content fetched or indexed. It rides only the standard BEP-51 verb.
//
// Exit contract: 0 when at least one node answered a sample query (even with
// zero infohashes), 1 when the crawl reached nobody (every seed/frontier node
// failed) — so a scripted crawl fails loudly rather than reporting silent
// success on a dead network (the same fail-on-all-fail rule as dht-smoke).
func cmdCrawl(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("crawl", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		seedList   stringSliceFlag
		workers    int
		maxIH      int
		maxNodes   int
		timeoutMs  int
		durationMs int
		asJSON     bool
	)
	fs.Var(&seedList, "seed", "seed DHT node host:port (repeatable; default: public bootstrap routers)")
	fs.IntVar(&workers, "workers", 8, "concurrent sample queries")
	fs.IntVar(&maxIH, "max-infohashes", 1000, "stop after this many unique infohashes (0 = until frontier drains)")
	fs.IntVar(&maxNodes, "max-nodes", 2048, "hard cap on total nodes visited")
	fs.IntVar(&timeoutMs, "timeout-ms", 8000, "per-sample query timeout in milliseconds")
	fs.IntVar(&durationMs, "duration-ms", 30000, "overall crawl time budget in milliseconds")
	fs.BoolVar(&asJSON, "json", false, "emit JSON instead of human text")
	if err := fs.Parse(args); err != nil {
		return parseErrExit(err)
	}
	if workers < 1 {
		fmt.Fprintln(stderr, "swartznet crawl: --workers must be >= 1")
		return exitUsage
	}

	seeds := []string(seedList)
	if len(seeds) == 0 {
		seeds = defaultCrawlSeeds
	}
	seedNodes, resolveErrs := resolveCrawlSeeds(seeds)
	if len(seedNodes) == 0 {
		fmt.Fprintf(stderr, "swartznet crawl: no seed node resolved (%d errors)\n", resolveErrs)
		return exitRuntime
	}

	// A throwaway loopback DHT server used only to send queries. Passive so we
	// never answer inbound; NoSecurity so an arbitrary node ID is fine (we only
	// query, we do not join routing tables).
	conn, err := net.ListenPacket("udp", "0.0.0.0:0")
	if err != nil {
		return reportRunErr(fmt.Errorf("bind udp: %w", err), stderr)
	}
	defer conn.Close()
	srv, err := dht.NewServer(&dht.ServerConfig{Conn: conn, NoSecurity: true, Passive: true})
	if err != nil {
		return reportRunErr(fmt.Errorf("dht.NewServer: %w", err), stderr)
	}
	defer srv.Close()

	// Stream discovered infohashes; collect for JSON, print live for text.
	var (
		mu    sync.Mutex
		found []string
	)
	sink := func(ih [20]byte) {
		s := hex.EncodeToString(ih[:])
		mu.Lock()
		found = append(found, s)
		mu.Unlock()
		if !asJSON {
			fmt.Fprintln(stdout, s)
		}
	}

	opts := dhtindex.CrawlOptions{
		Workers:       workers,
		MaxFrontier:   maxNodes,
		MaxInfohashes: maxIH,
		PerQuery:      time.Duration(timeoutMs) * time.Millisecond,
	}
	crawler := dhtindex.NewCrawler(srv, sink, opts, newLogger(stderr))

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(durationMs)*time.Millisecond)
	defer cancel()
	if !asJSON {
		fmt.Fprintf(stderr, "crawling %d seed(s), up to %d infohashes / %d nodes, %s budget…\n",
			len(seedNodes), maxIH, maxNodes, time.Duration(durationMs)*time.Millisecond)
	}
	stats := crawler.CrawlOnce(ctx, seedNodes)

	if asJSON {
		mu.Lock()
		out := struct {
			Seeds          int      `json:"seeds"`
			NodesSampled   int      `json:"nodes_sampled"`
			NodesErrored   int      `json:"nodes_errored"`
			InfohashesSeen int      `json:"infohashes_seen"`
			Infohashes     []string `json:"infohashes"`
		}{
			Seeds:          len(seedNodes),
			NodesSampled:   stats.NodesSampled,
			NodesErrored:   stats.NodesErrored,
			InfohashesSeen: stats.InfohashesSeen,
			Infohashes:     found,
		}
		mu.Unlock()
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return reportRunErr(err, stderr)
		}
	} else {
		fmt.Fprintf(stderr, "done: %d nodes sampled, %d errored, %d unique infohashes\n",
			stats.NodesSampled, stats.NodesErrored, stats.InfohashesSeen)
	}

	if stats.NodesSampled == 0 {
		fmt.Fprintln(stderr, "swartznet crawl: reached no DHT nodes (network down or all seeds unreachable)")
		return exitRuntime
	}
	return exitOK
}

// resolveCrawlSeeds resolves each host:port seed to a CrawlNode, returning the
// resolved nodes and a count of resolution failures. A seed node's ID is unknown
// until it replies, so a random placeholder ID is used (the crawler keys on the
// address and queries with a random target, never the node's ID).
func resolveCrawlSeeds(seeds []string) ([]dhtindex.CrawlNode, int) {
	var (
		nodes []dhtindex.CrawlNode
		errs  int
		seen  = map[string]struct{}{}
	)
	for _, s := range seeds {
		udp, err := net.ResolveUDPAddr("udp", s)
		if err != nil || udp.IP == nil || udp.Port == 0 {
			errs++
			continue
		}
		key := udp.String()
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		var id krpc.ID // placeholder; unused for the query
		nodes = append(nodes, dhtindex.CrawlNode{Addr: dht.NewAddr(udp), ID: id})
	}
	return nodes, errs
}
