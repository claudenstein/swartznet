package testlab

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
	"github.com/swartznet/swartznet/internal/indexer"
)

// NewDHTCluster spawns n engine.Engine instances on loopback with
// the mainline DHT actually enabled — unlike NewCluster, which
// disables DHT so scenario tests can run fast. The nodes are
// chain-bootstrapped so they form a live intra-cluster DHT without
// ever reaching the public mainline routers:
//
//   - node 0 has no bootstrap list (it's the root);
//   - node 1 bootstraps to node 0 only (avoids the self-bootstrap
//     failure mode documented in testbed/docker-compose.dht.yml);
//   - nodes 2..n-1 bootstrap to both node 0 and node 1.
//
// Every node runs with NoSecurity=true (BEP-42 node-ID security
// disabled) — loopback IPs can never produce a "secure" node ID,
// so puts would silently filter every target otherwise. The same
// trade-off applies to the docker testbed (see
// config.DHTInsecure).
//
// This is the in-process counterpart to testbed scenario s12. It
// lets Layer-D scenarios exercise the full engine wiring — real
// Publisher, real Lookup, real anacrolix getput — without any
// docker or live mainline dependency. If a bug reproduces here,
// it's in the engine-level code; if it only shows up in s12, it's
// in the docker-bridge networking layer.
//
// The shared fake swarm (SharedInfoHash) is still added to every
// node so the sn_search LTEP handshake path exists alongside DHT.
// Tests that don't care about sn_search can just ignore it.
func NewDHTCluster(t *testing.T, n int) *Cluster {
	t.Helper()
	if n < 1 {
		t.Fatalf("testlab.NewDHTCluster: n=%d, want >=1", n)
	}
	c := &Cluster{
		t:        t,
		sharedIH: sharedInfoHash(),
	}
	for i := 0; i < n; i++ {
		node := c.spawnDHTNode(t, i, c.bootstrapsFor(i))
		c.Nodes = append(c.Nodes, node)
	}
	t.Cleanup(c.Stop)
	return c
}

// bootstrapsFor returns the DHT bootstrap "host:port" list node i
// should use. See NewDHTCluster's docstring for the topology
// rationale.
//
// Node 0 gets a sentinel placeholder (127.0.0.1:1) rather than an
// empty list. Empty DHTBootstrapAddrs falls through to anacrolix's
// GlobalBootstrapAddrs default (router.bittorrent.com et al.),
// which would poison the isolated cluster with routes from the
// live mainline DHT — observed as "put errors to 5.196.x.x" in
// the first iteration of this harness. The placeholder is a
// routable UDP address with no listener, so dht.Server queues one
// timed-out find_node against it at startup and otherwise leaves
// node 0 with an empty routing table until node 1 dials it.
func (c *Cluster) bootstrapsFor(i int) []string {
	const placeholder = "127.0.0.1:1"
	switch {
	case i == 0:
		return []string{placeholder}
	case i == 1:
		return []string{dhtBootstrapAddr(c.Nodes[0])}
	default:
		return []string{
			dhtBootstrapAddr(c.Nodes[0]),
			dhtBootstrapAddr(c.Nodes[1]),
		}
	}
}

// dhtBootstrapAddr returns a "host:port" string for node n's
// DHT listener. The engine binds DHT to 0.0.0.0 on an OS-assigned
// port; we rewrite the IP to 127.0.0.1 so loopback routing stays
// unambiguous regardless of which interface the kernel picks.
func dhtBootstrapAddr(n *Node) string {
	if n == nil || n.Eng == nil {
		return ""
	}
	addr := n.Eng.DHTAddr()
	if addr == nil {
		return ""
	}
	udp, ok := addr.(*net.UDPAddr)
	if !ok {
		return addr.String()
	}
	return fmt.Sprintf("127.0.0.1:%d", udp.Port)
}

// spawnDHTNode is spawnNode's DHT-enabled sibling. The two share
// the same on-disk layout (data/bleve/companion under t.TempDir())
// but this variant flips the engine's DHT knobs: DisableDHT=false,
// DHTInsecure=true, bootstrap addrs explicitly wired.
func (c *Cluster) spawnDHTNode(t *testing.T, idx int, bootstraps []string) *Node {
	t.Helper()
	root := filepath.Join(t.TempDir(), fmt.Sprintf("dht-node-%d", idx))

	cfg := config.Default()
	cfg.DataDir = filepath.Join(root, "data")
	cfg.IndexDir = filepath.Join(root, "bleve")
	cfg.IdentityPath = filepath.Join(root, "identity.key")
	cfg.PublisherManifest = filepath.Join(root, "publisher.json")
	cfg.ReputationPath = filepath.Join(root, "reputation.json")
	cfg.SeedListPath = ""
	cfg.BloomPath = filepath.Join(root, "known-good.bloom")
	cfg.CompanionDir = filepath.Join(root, "companion")
	cfg.CompanionFollowFile = filepath.Join(root, "follows.json")
	cfg.ListenPort = 0

	// DHT ON with NoSecurity — the whole point of this variant.
	cfg.DisableDHT = false
	cfg.DHTInsecure = true
	cfg.DHTBootstrapAddrs = append([]string(nil), bootstraps...)
	// Force IPv4-only for the torrent client so we end up with a
	// single DHT server on a udp4 listener. Anacrolix otherwise
	// spins up both udp4 and udp6 listeners, creates a DHT
	// server for each, and Engine.dhtServer() picks just one —
	// leaving the other half of each node's address space
	// inhabited by a DHT no traversal uses. Worse, cross-node
	// traversal pollutes routing tables with IPv4-mapped-IPv6
	// addresses like [::ffff:127.0.0.1]:X that the publisher
	// then tries (and fails) to put to. Stripping IPv6 keeps
	// the harness on one address family the whole way through.
	cfg.DisableIPv6 = true
	// Bind every listener to 127.0.0.1 (not the anacrolix
	// default 0.0.0.0). The DHT's BEP-44 put-token validator
	// SHA1s the source IP of the query that issued the token;
	// when a socket is bound to 0.0.0.0 the kernel is free to
	// pick a different source IP per send, and get-then-put
	// sequences trip the "invalid token" branch — observed as
	// "value not found" on the reader side with no visible
	// error on the sender. Loopback bind pins the source to
	// 127.0.0.1 and makes the validator deterministic.
	cfg.ListenHost = "127.0.0.1"

	cfg.Seed = true
	cfg.NoUpload = false
	cfg.Regtest = true

	buf := &syncedBuffer{}
	log := slog.New(slog.NewTextHandler(io.MultiWriter(buf, io.Discard), &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	eng, err := engine.New(context.Background(), cfg, log)
	if err != nil {
		t.Fatalf("testlab: spawn DHT node %d: %v", idx, err)
	}

	bleveDir := filepath.Join(root, "bleve-idx")
	index, err := indexer.Open(bleveDir)
	if err != nil {
		eng.Close()
		t.Fatalf("testlab: open index DHT node %d: %v", idx, err)
	}
	eng.SetIndex(index)

	if _, err := eng.AddInfoHash(c.sharedIH); err != nil {
		index.Close()
		eng.Close()
		t.Fatalf("testlab: add shared infohash DHT node %d: %v", idx, err)
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
