package dhtindex

import (
	"context"
	"crypto/rand"
	"log/slog"
	"sync"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/krpc"
)

// This file owns the BEP-51 crawler worker-pool: a bounded, deterministic
// breadth-first walk of the DHT that samples infohashes from each node it
// reaches and expands its frontier from the neighbours those nodes return. It
// rides the standard BEP-51 sample_infohashes verb (no new verb/bit/port) and
// composes the narrow SampleInfohashes primitive. Control is pure Go — the loop,
// dedup, politeness bounds, and stop conditions are code, never an LLM.
//
// The crawler only DISCOVERS infohashes and streams them to a sink; it never
// fetches metainfo, admits, indexes, or downloads. Turning discovered
// infohashes into indexed content is a separate, policy-laden step that stays
// above this component (fail-closed admission owns that decision).

// CrawlNode is one frontier entry: a DHT node to sample.
type CrawlNode struct {
	Addr dht.Addr
	ID   krpc.ID
}

func (n CrawlNode) key() string {
	if n.Addr == nil {
		return ""
	}
	return n.Addr.String()
}

// sampleFunc issues ONE BEP-51 sample_infohashes query. Real crawls bind it to
// a dht.Server via SampleInfohashes; tests inject a deterministic fake so the
// worker-pool is exercised without a live DHT.
type sampleFunc func(ctx context.Context, addr dht.Addr, target krpc.ID) (SampleInfohashesResult, error)

// CrawlOptions bounds a crawl. Every field has a safe default so a zero value is
// usable. The bounds are what keep a crawl polite and finite: a fresh crawl
// visits each reachable node at most once (dedup by address), never exceeds
// MaxFrontier total visits, runs at most Workers samples concurrently, times
// each sample out after PerQuery, and stops expanding once MaxInfohashes unique
// infohashes have been seen.
type CrawlOptions struct {
	Workers       int           // concurrent samples (default 4)
	MaxFrontier   int           // hard cap on total nodes visited (default 4096)
	MaxInfohashes int           // stop once this many unique IHs seen (0 = until frontier drains / ctx)
	PerQuery      time.Duration // per-sample timeout (default 8s)
}

func (o CrawlOptions) withDefaults() CrawlOptions {
	if o.Workers < 1 {
		o.Workers = 4
	}
	if o.MaxFrontier < 1 {
		o.MaxFrontier = 4096
	}
	if o.PerQuery <= 0 {
		o.PerQuery = 8 * time.Second
	}
	return o
}

// CrawlStats is the outcome of a CrawlOnce pass.
type CrawlStats struct {
	NodesSampled   int // nodes that answered a sample query
	NodesErrored   int // nodes whose sample query failed/timed out
	InfohashesSeen int // unique infohashes streamed to the sink
}

// Crawler is a bounded BEP-51 sample_infohashes worker pool.
type Crawler struct {
	sample sampleFunc
	sink   func(ih [20]byte)
	opts   CrawlOptions
	log    *slog.Logger

	mu      sync.Mutex
	queued  map[string]struct{}   // addr → already enqueued (visit-once dedup)
	seenIH  map[[20]byte]struct{} // unique-infohash dedup
	sampled int
	errored int
}

// NewCrawler binds a crawler to a live dht.Server. sink receives each unique
// discovered infohash (may be nil to only collect stats). A nil server yields a
// crawler whose samples all error — callers should pass a bootstrapped server.
func NewCrawler(server *dht.Server, sink func(ih [20]byte), opts CrawlOptions, log *slog.Logger) *Crawler {
	sample := func(ctx context.Context, addr dht.Addr, target krpc.ID) (SampleInfohashesResult, error) {
		return SampleInfohashes(ctx, server, addr, target)
	}
	return newCrawler(sample, sink, opts, log)
}

// newCrawler is the injectable constructor (tests pass a fake sampleFunc).
func newCrawler(sample sampleFunc, sink func(ih [20]byte), opts CrawlOptions, log *slog.Logger) *Crawler {
	if log == nil {
		log = slog.Default()
	}
	return &Crawler{
		sample: sample,
		sink:   sink,
		opts:   opts.withDefaults(),
		log:    log,
		queued: make(map[string]struct{}),
		seenIH: make(map[[20]byte]struct{}),
	}
}

// CrawlOnce runs one bounded breadth-first pass from seeds, returning when the
// frontier drains, MaxInfohashes is reached, or ctx is cancelled. It is safe to
// call once per Crawler (the dedup sets are not reset between calls; construct a
// fresh Crawler for an independent pass).
func (c *Crawler) CrawlOnce(ctx context.Context, seeds []CrawlNode) CrawlStats {
	sem := make(chan struct{}, c.opts.Workers)
	var wg sync.WaitGroup

	var visit func(n CrawlNode)
	visit = func(n CrawlNode) {
		defer wg.Done()
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		defer func() { <-sem }()
		if ctx.Err() != nil {
			return
		}
		qctx, cancel := context.WithTimeout(ctx, c.opts.PerQuery)
		res, err := c.sample(qctx, n.Addr, randomTarget())
		cancel()
		if err != nil {
			c.recordErr()
			return
		}
		c.recordSampled()
		for _, raw := range res.Samples {
			c.emit([20]byte(raw))
		}
		for _, ni := range res.Nodes {
			child, ok := toCrawlNode(ni)
			if !ok {
				continue
			}
			if c.tryEnqueue(child.key()) {
				wg.Add(1)
				go visit(child)
			}
		}
	}

	for _, s := range seeds {
		if c.tryEnqueue(s.key()) {
			wg.Add(1)
			go visit(s)
		}
	}
	wg.Wait()
	return c.snapshot()
}

// tryEnqueue gates a node into the frontier: false if the infohash target is
// already met, the frontier cap is reached, the address is empty, or the node
// was already visited. This is the single place the crawl's size is bounded.
func (c *Crawler) tryEnqueue(key string) bool {
	if key == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.opts.MaxInfohashes > 0 && len(c.seenIH) >= c.opts.MaxInfohashes {
		return false
	}
	if len(c.queued) >= c.opts.MaxFrontier {
		return false
	}
	if _, ok := c.queued[key]; ok {
		return false
	}
	c.queued[key] = struct{}{}
	return true
}

// emit dedups an infohash, enforces the MaxInfohashes ceiling, and streams new
// ones to the sink (called off-lock so a slow sink can't stall the workers).
func (c *Crawler) emit(ih [20]byte) {
	c.mu.Lock()
	if _, dup := c.seenIH[ih]; dup {
		c.mu.Unlock()
		return
	}
	if c.opts.MaxInfohashes > 0 && len(c.seenIH) >= c.opts.MaxInfohashes {
		c.mu.Unlock()
		return
	}
	c.seenIH[ih] = struct{}{}
	c.mu.Unlock()
	if c.sink != nil {
		c.sink(ih)
	}
}

func (c *Crawler) recordSampled() { c.mu.Lock(); c.sampled++; c.mu.Unlock() }
func (c *Crawler) recordErr()     { c.mu.Lock(); c.errored++; c.mu.Unlock() }

func (c *Crawler) snapshot() CrawlStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return CrawlStats{NodesSampled: c.sampled, NodesErrored: c.errored, InfohashesSeen: len(c.seenIH)}
}

// toCrawlNode converts a BEP-51 neighbour into a frontier entry, skipping a node
// with no usable UDP address.
func toCrawlNode(ni krpc.NodeInfo) (CrawlNode, bool) {
	udp := ni.Addr.UDP()
	if udp == nil || udp.IP == nil || udp.Port == 0 {
		return CrawlNode{}, false
	}
	return CrawlNode{Addr: dht.NewAddr(udp), ID: ni.ID}, true
}

// randomTarget picks a uniform 20-byte node ID so successive samples cover
// different keyspace slices. A crypto/rand hiccup degrades to the zero target
// (still a valid query), never a panic.
func randomTarget() krpc.ID {
	var id krpc.ID
	_, _ = rand.Read(id[:])
	return id
}
