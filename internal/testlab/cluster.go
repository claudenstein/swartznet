package testlab

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
	"github.com/swartznet/swartznet/internal/indexer"
	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// Cluster is an in-process mesh of N *engine.Engine instances.
// Each node has its own Bleve index, identity, data dir, and
// listen port. Nodes discover each other via WireMesh (no DHT,
// no tracker) and exchange LTEP handshakes when they connect,
// at which point the sn_search extension is advertised on
// every pair.
//
// Concurrency: Cluster methods are not safe for concurrent use
// after Stop returns. Within a single test they are safe to
// call from the main goroutine.
type Cluster struct {
	t     *testing.T
	Nodes []*Node

	// sharedIH is the infohash of the dummy "swarm" every node
	// joins so they can enter each other's peer-wire protocol.
	// anacrolix's sn_search LTEP extension only fires inside a
	// torrent swarm; the torrent's actual metadata/content is
	// irrelevant here.
	//
	// We use AddInfoHash (not AddTorrentMetaInfo) because nodes
	// don't actually have the payload on disk — anacrolix will
	// open peer-wire connections to fetch metadata from peers,
	// and the LTEP handshake (which is what sn_search rides on)
	// happens before any metadata is exchanged. So the handshake
	// fires even though the metadata fetch itself never
	// completes.
	sharedIH [20]byte
}

// Node wraps a single *engine.Engine plus the per-node state a
// test needs to inspect (index handle, identity, per-node
// logger output). All state-directory fields are absolute paths
// rooted at t.TempDir() so scenario tests can locate files on
// disk without reaching into the engine's config.
type Node struct {
	Eng          *engine.Engine
	Index        *indexer.Index
	LogBuf       *syncedBuffer
	DataDir      string
	IndexDir     string
	companionDir string
}

// CompanionDir returns the directory the node would use for
// the F3 companion publisher's on-disk artefacts (gzipped JSON
// content index + wrapping .torrent). Scenario tests use it
// when constructing a companion.Publisher directly rather than
// relying on the engine's automatic wiring.
func (n *Node) CompanionDir() string { return n.companionDir }

// syncedBuffer is a bytes.Buffer with a mutex so per-node
// loggers don't race when the test goroutine reads the tail.
type syncedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncedBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

// String returns a snapshot of everything written so far.
func (s *syncedBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// NewCluster builds a Cluster of n nodes and registers a
// t.Cleanup hook to shut them down automatically at test end.
// Every node runs with DHT disabled (tests don't need it —
// Layer D is exercised by substituting a fake Putter/Getter,
// not by connecting to real DHT nodes).
//
// If n < 1 the call panics; tests should always ask for at
// least one node.
func NewCluster(t *testing.T, n int) *Cluster {
	t.Helper()
	if n < 1 {
		t.Fatalf("testlab.NewCluster: n=%d, want >=1", n)
	}

	// Deterministic shared infohash. Every testlab cluster
	// uses the same 20 bytes because each process runs its own
	// isolated mainline DHT (we disable DHT entirely) so there
	// is no risk of cross-cluster collision.
	c := &Cluster{
		t:        t,
		sharedIH: sharedInfoHash(),
	}
	for i := 0; i < n; i++ {
		n := c.spawnNode(t, i)
		c.Nodes = append(c.Nodes, n)
	}
	t.Cleanup(c.Stop)
	return c
}

// spawnNode constructs one Node with per-instance state dirs
// rooted at t.TempDir(). Returns a fully-started *engine.Engine
// bound to a shared per-node log buffer.
func (c *Cluster) spawnNode(t *testing.T, idx int) *Node {
	t.Helper()
	root := filepath.Join(t.TempDir(), fmt.Sprintf("node-%d", idx))

	cfg := config.Default()
	cfg.DataDir = filepath.Join(root, "data")
	cfg.IndexDir = filepath.Join(root, "bleve")
	cfg.IdentityPath = filepath.Join(root, "identity.key")
	cfg.PublisherManifest = filepath.Join(root, "publisher.json")
	cfg.ReputationPath = filepath.Join(root, "reputation.json")
	cfg.SeedListPath = "" // tests opt in to seed lists explicitly
	cfg.BloomPath = filepath.Join(root, "known-good.bloom")
	cfg.CompanionDir = filepath.Join(root, "companion")
	cfg.CompanionFollowFile = filepath.Join(root, "follows.json")
	cfg.ListenPort = 0 // OS-assigned
	cfg.DisableDHT = true
	// Seed = true so scenario tests that actually transfer
	// data between nodes (e.g. the M14e F3 companion scenario)
	// can do so — anacrolix won't serve data to peers when
	// Seed is false. NoUpload is left at its zero value
	// (false) for the same reason.
	cfg.Seed = true
	cfg.NoUpload = false
	// M15a: regtest mode accelerates every production timer so
	// scenario tests see publish/refresh events in seconds
	// instead of hours. Mandatory for the testlab harness —
	// without it the M14e F3 companion scenario would need to
	// wait 3600s for the first refresh tick.
	cfg.Regtest = true

	buf := &syncedBuffer{}
	// Debug-level logging helps when a scenario fails to
	// handshake or a query never receives responses — the
	// captured buffer is printed by dumpLogs(t) on demand.
	log := slog.New(slog.NewTextHandler(io.MultiWriter(buf, io.Discard), &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	eng, err := engine.New(context.Background(), cfg, log)
	if err != nil {
		t.Fatalf("testlab: spawn node %d: %v", idx, err)
	}

	// Always-on local index for the node. Tests expect every
	// node to have its own Bleve directory so Layer-L queries
	// have something to answer against.
	bleveDir := filepath.Join(root, "bleve-idx")
	index, err := indexer.Open(bleveDir)
	if err != nil {
		eng.Close()
		t.Fatalf("testlab: open index node %d: %v", idx, err)
	}
	eng.SetIndex(index)

	// Every node joins a shared swarm via infohash-only add.
	// No metadata, no content on disk — anacrolix will dial
	// peers to request metadata, and the LTEP handshake (which
	// is where sn_search fires) happens before any metadata
	// exchange. The metadata fetch itself will never complete
	// because nobody has it, but that's fine for these tests.
	if _, err := eng.AddInfoHash(c.sharedIH); err != nil {
		index.Close()
		eng.Close()
		t.Fatalf("testlab: add shared infohash node %d: %v", idx, err)
	}

	return &Node{
		Eng:          eng,
		Index:        index,
		LogBuf:       buf,
		DataDir:      cfg.DataDir,
		IndexDir:     cfg.IndexDir,
		companionDir: cfg.CompanionDir,
	}
}

// Stop closes every node's engine + index in reverse order.
// Idempotent; running t.Cleanup will invoke it automatically.
func (c *Cluster) Stop() {
	for i := len(c.Nodes) - 1; i >= 0; i-- {
		n := c.Nodes[i]
		if n == nil {
			continue
		}
		if n.Eng != nil {
			_ = n.Eng.Close()
		}
		if n.Index != nil {
			_ = n.Index.Close()
		}
	}
	c.Nodes = nil
}

// SharedInfoHash returns the 20-byte infohash of the dummy
// swarm every node in the cluster is a member of. Tests that
// want to reach the underlying anacrolix *Torrent use this to
// look the handle up.
func (c *Cluster) SharedInfoHash() [20]byte { return c.sharedIH }

// DumpLogs writes every node's buffered log output to t.Log.
// Useful in tests that need to debug handshake races or
// query-path stalls — call on failure paths.
func (c *Cluster) DumpLogs(t *testing.T) {
	t.Helper()
	for i, n := range c.Nodes {
		t.Logf("--- node %d port=%d ---\n%s", i, n.Eng.LocalPort(), n.LogBuf.String())
	}
}

// WireMesh adds every node's listen address to every other
// node's peer set for the shared swarm, so all pairs attempt a
// TCP handshake as soon as the torrent client's dial loop
// picks them up. O(N²) connections — fine for small clusters,
// expensive for n > 20.
//
// Must be called AFTER every node has added the shared torrent
// (NewCluster does this automatically).
func (c *Cluster) WireMesh(t *testing.T) {
	t.Helper()
	for i, a := range c.Nodes {
		for j, b := range c.Nodes {
			if i == j {
				continue
			}
			if _, err := a.Eng.AddTrustedPeerEngine(c.sharedIH, b.Eng); err != nil {
				t.Fatalf("testlab: wire %d→%d: %v", i, j, err)
			}
		}
	}
}

// WaitAllHandshaked polls the sn_search peer book on every node
// until every other node in the cluster shows up as a capable
// peer, or timeout elapses. Fails the test on timeout. This is
// the usual "ready" gate before running Layer-S queries.
func (c *Cluster) WaitAllHandshaked(t *testing.T, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	want := len(c.Nodes) - 1
	for time.Now().Before(deadline) {
		allDone := true
		for _, n := range c.Nodes {
			capable := countCapablePeers(n.Eng.SwarmSearch())
			if capable < want {
				allDone = false
				break
			}
		}
		if allDone {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Timed out — dump per-node state so the failure message is
	// debuggable. Show ALL known peers (not just capable) so we
	// can tell whether peers are connecting at all vs. connecting
	// but not advertising sn_search.
	var lines []string
	for i, n := range c.Nodes {
		p := n.Eng.SwarmSearch()
		known := 0
		capable := 0
		if p != nil {
			known = len(p.KnownPeers())
			capable = p.CapablePeerCount()
		}
		lines = append(lines, fmt.Sprintf("  node %d port=%d known=%d capable=%d want %d",
			i, n.Eng.LocalPort(), known, capable, want))
	}
	t.Fatalf("testlab: not all nodes handshook within %s:\n%s",
		timeout, joinLines(lines))
}

// countCapablePeers wraps Protocol.CapablePeerCount in a
// defensive nil-check — early in startup the swarm pointer is
// always non-nil but we want the helper to be robust.
func countCapablePeers(p *swarmsearch.Protocol) int {
	if p == nil {
		return 0
	}
	return p.CapablePeerCount()
}

func joinLines(lines []string) string {
	out := ""
	for _, s := range lines {
		out += s + "\n"
	}
	return out
}

// sharedInfoHash is the 20-byte constant every testlab cluster
// uses as the shared "swarm" target. Not real content — no
// node has metadata or payload for this infohash. It exists
// only so anacrolix will open peer-wire connections between
// the cluster nodes, at which point the LTEP handshake fires
// and sn_search is advertised.
//
// The value spells "swartznet-testlab1" in ASCII, padded to 20
// bytes. Any deterministic non-zero value would work; this one
// is just easy to recognise in log lines.
func sharedInfoHash() [20]byte {
	var out [20]byte
	copy(out[:], []byte("swartznet-testlab-01"))
	return out
}

// used is a no-op reference to silence unused-import warnings
// during future refactors that re-introduce bytes/metainfo.
var _ = bytes.NewReader
