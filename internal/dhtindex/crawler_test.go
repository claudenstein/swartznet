package dhtindex

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/krpc"
)

// fakeDHT is a deterministic in-memory DHT graph for exercising the crawler
// worker-pool without a live network: it maps a node address to the samples +
// neighbours that node returns, and records how many times each node is queried.
type fakeDHT struct {
	mu      sync.Mutex
	nodes   map[string]fakeReply
	errAddr map[string]bool // addresses whose sample query fails
	queried map[string]int
}

type fakeReply struct {
	samples []krpc.ID
	peers   []int // neighbour ports (127.0.0.1:port)
}

func newFakeDHT() *fakeDHT {
	return &fakeDHT{nodes: map[string]fakeReply{}, errAddr: map[string]bool{}, queried: map[string]int{}}
}

func addrFor(port int) dht.Addr {
	return dht.NewAddr(&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
}

func nodeFor(port int) CrawlNode {
	var id krpc.ID
	id[0] = byte(port)
	return CrawlNode{Addr: addrFor(port), ID: id}
}

func ihN(n byte) krpc.ID {
	var id krpc.ID
	id[0] = n
	return id
}

func (f *fakeDHT) sample(_ context.Context, addr dht.Addr, _ krpc.ID) (SampleInfohashesResult, error) {
	key := addr.String()
	f.mu.Lock()
	f.queried[key]++
	reply, ok := f.nodes[key]
	fail := f.errAddr[key]
	f.mu.Unlock()
	if fail {
		return SampleInfohashesResult{}, errors.New("sample failed")
	}
	if !ok {
		return SampleInfohashesResult{}, nil
	}
	out := SampleInfohashesResult{Samples: reply.samples}
	for _, p := range reply.peers {
		out.Nodes = append(out.Nodes, krpc.NodeInfo{
			ID:   nodeFor(p).ID,
			Addr: krpc.NodeAddr{IP: net.IPv4(127, 0, 0, 1), Port: p},
		})
	}
	return out, nil
}

func (f *fakeDHT) queryCount(port int) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.queried[addrFor(port).String()]
}

// collectSink returns a sink and a func to read the deduped infohashes it saw.
func collectSink() (func([20]byte), func() map[[20]byte]int) {
	var mu sync.Mutex
	seen := map[[20]byte]int{}
	return func(ih [20]byte) {
			mu.Lock()
			seen[ih]++
			mu.Unlock()
		}, func() map[[20]byte]int {
			mu.Lock()
			defer mu.Unlock()
			out := map[[20]byte]int{}
			for k, v := range seen {
				out[k] = v
			}
			return out
		}
}

// TestCrawlOnceDiscoversAndExpands: seed A yields ih1,ih2 + neighbours B,C; B
// yields ih3; C yields ih2(dup),ih4. The crawl must discover all four unique
// infohashes exactly once and sample all three nodes.
func TestCrawlOnceDiscoversAndExpands(t *testing.T) {
	t.Parallel()
	f := newFakeDHT()
	f.nodes[addrFor(1).String()] = fakeReply{samples: []krpc.ID{ihN(1), ihN(2)}, peers: []int{2, 3}}
	f.nodes[addrFor(2).String()] = fakeReply{samples: []krpc.ID{ihN(3)}}
	f.nodes[addrFor(3).String()] = fakeReply{samples: []krpc.ID{ihN(2), ihN(4)}}

	sink, read := collectSink()
	c := newCrawler(f.sample, sink, CrawlOptions{Workers: 3}, nil)
	stats := c.CrawlOnce(context.Background(), []CrawlNode{nodeFor(1)})

	if stats.NodesSampled != 3 {
		t.Errorf("NodesSampled = %d, want 3", stats.NodesSampled)
	}
	if stats.InfohashesSeen != 4 {
		t.Errorf("InfohashesSeen = %d, want 4", stats.InfohashesSeen)
	}
	seen := read()
	if len(seen) != 4 {
		t.Fatalf("sink saw %d unique infohashes, want 4", len(seen))
	}
	for ih, n := range seen {
		if n != 1 {
			t.Errorf("infohash %x emitted %d times, want exactly 1 (dedup)", ih[:2], n)
		}
	}
}

// TestCrawlOnceDedupsNodesInACycle: A↔B cycle must terminate, each node sampled
// exactly once.
func TestCrawlOnceDedupsNodesInACycle(t *testing.T) {
	t.Parallel()
	f := newFakeDHT()
	f.nodes[addrFor(1).String()] = fakeReply{samples: []krpc.ID{ihN(1)}, peers: []int{2}}
	f.nodes[addrFor(2).String()] = fakeReply{samples: []krpc.ID{ihN(2)}, peers: []int{1}} // back to A

	c := newCrawler(f.sample, nil, CrawlOptions{Workers: 2}, nil)
	stats := c.CrawlOnce(context.Background(), []CrawlNode{nodeFor(1)})

	if stats.NodesSampled != 2 {
		t.Errorf("NodesSampled = %d, want 2 (cycle must not re-sample)", stats.NodesSampled)
	}
	if q := f.queryCount(1); q != 1 {
		t.Errorf("node A queried %d times, want 1", q)
	}
}

// TestCrawlOnceMaxFrontierCap bounds total nodes visited.
func TestCrawlOnceMaxFrontierCap(t *testing.T) {
	t.Parallel()
	f := newFakeDHT()
	f.nodes[addrFor(1).String()] = fakeReply{samples: []krpc.ID{ihN(1)}, peers: []int{2, 3, 4, 5}}
	for _, p := range []int{2, 3, 4, 5} {
		f.nodes[addrFor(p).String()] = fakeReply{samples: []krpc.ID{ihN(byte(p))}}
	}
	c := newCrawler(f.sample, nil, CrawlOptions{Workers: 1, MaxFrontier: 2}, nil)
	stats := c.CrawlOnce(context.Background(), []CrawlNode{nodeFor(1)})
	if stats.NodesSampled > 2 {
		t.Errorf("NodesSampled = %d, want <= 2 (MaxFrontier)", stats.NodesSampled)
	}
}

// TestCrawlOnceRespectsMaxInfohashes stops expanding once the target is met.
// Workers=1 makes the ceiling exact (no concurrent overshoot).
func TestCrawlOnceRespectsMaxInfohashes(t *testing.T) {
	t.Parallel()
	f := newFakeDHT()
	f.nodes[addrFor(1).String()] = fakeReply{samples: []krpc.ID{ihN(1), ihN(2)}, peers: []int{2}}
	f.nodes[addrFor(2).String()] = fakeReply{samples: []krpc.ID{ihN(3), ihN(4)}, peers: []int{3}}
	f.nodes[addrFor(3).String()] = fakeReply{samples: []krpc.ID{ihN(5), ihN(6)}}

	sink, read := collectSink()
	c := newCrawler(f.sample, sink, CrawlOptions{Workers: 1, MaxInfohashes: 2}, nil)
	stats := c.CrawlOnce(context.Background(), []CrawlNode{nodeFor(1)})
	if stats.InfohashesSeen != 2 {
		t.Errorf("InfohashesSeen = %d, want exactly 2 (MaxInfohashes, Workers=1)", stats.InfohashesSeen)
	}
	if len(read()) != 2 {
		t.Errorf("sink saw %d, want 2", len(read()))
	}
}

// TestCrawlOnceCountsErrors: a failing node is counted, the crawl continues.
func TestCrawlOnceCountsErrors(t *testing.T) {
	t.Parallel()
	f := newFakeDHT()
	f.nodes[addrFor(1).String()] = fakeReply{samples: []krpc.ID{ihN(1)}, peers: []int{2}}
	f.errAddr[addrFor(2).String()] = true // B fails
	c := newCrawler(f.sample, nil, CrawlOptions{Workers: 2}, nil)
	stats := c.CrawlOnce(context.Background(), []CrawlNode{nodeFor(1)})
	if stats.NodesSampled != 1 {
		t.Errorf("NodesSampled = %d, want 1", stats.NodesSampled)
	}
	if stats.NodesErrored != 1 {
		t.Errorf("NodesErrored = %d, want 1", stats.NodesErrored)
	}
	if stats.InfohashesSeen != 1 {
		t.Errorf("InfohashesSeen = %d, want 1", stats.InfohashesSeen)
	}
}

// TestCrawlOnceContextCancel returns promptly on a cancelled context.
func TestCrawlOnceContextCancel(t *testing.T) {
	t.Parallel()
	f := newFakeDHT()
	f.nodes[addrFor(1).String()] = fakeReply{samples: []krpc.ID{ihN(1)}, peers: []int{2, 3}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	c := newCrawler(f.sample, nil, CrawlOptions{Workers: 2}, nil)
	stats := c.CrawlOnce(ctx, []CrawlNode{nodeFor(1)})
	// Nothing should have been sampled (workers short-circuit on ctx.Err).
	if stats.NodesSampled != 0 {
		t.Errorf("NodesSampled = %d on a cancelled ctx, want 0", stats.NodesSampled)
	}
}

// TestCrawlOnceSkipsBadNeighbours: a neighbour with no usable address is skipped.
func TestCrawlOnceSkipsBadNeighbours(t *testing.T) {
	t.Parallel()
	f := newFakeDHT()
	// A returns one good neighbour port and the sampler will also be asked about
	// a zero-port neighbour, which toCrawlNode must reject.
	key := addrFor(1).String()
	f.mu.Lock()
	f.queried[key] = 0
	f.mu.Unlock()
	// Build a reply by hand so we can inject a bad NodeInfo (port 0).
	f.nodes[key] = fakeReply{samples: []krpc.ID{ihN(1)}, peers: []int{0, 2}}
	f.nodes[addrFor(2).String()] = fakeReply{samples: []krpc.ID{ihN(2)}}
	c := newCrawler(f.sample, nil, CrawlOptions{Workers: 2}, nil)
	stats := c.CrawlOnce(context.Background(), []CrawlNode{nodeFor(1)})
	// Only A and the good neighbour (port 2) sampled; the port-0 neighbour skipped.
	if stats.NodesSampled != 2 {
		t.Errorf("NodesSampled = %d, want 2 (bad neighbour skipped)", stats.NodesSampled)
	}
}
