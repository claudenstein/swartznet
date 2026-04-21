package engine_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/int160"
	"github.com/anacrolix/dht/v2/krpc"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// TestDHTWireCompatVanillaKRPC closes three rows of the
// vanilla-client interop matrix in one go (docs/05-integration-design
// §8 rows 8.3-A / 8.3-B / 8.3-C): a plain anacrolix dht.Server acting
// as "any BEP-5 client" must be able to ping, get_peers, and
// announce_peer against a running swartznet engine without any
// swartznet-specific knowledge.
//
// The test stands up:
//
//  1. A swartznet Engine with DHT enabled on a loopback-bound
//     UDP listener (cfg.DisableDHT = false).
//  2. A separate anacrolix dht.Server bound to another loopback
//     port, used as the "vanilla" BEP-5 client.
//
// It then drives the three query types the wire-compat matrix
// calls out and asserts each gets a valid r-type KRPC reply. If
// swartznet ever ships a mutation that breaks mainline KRPC (e.g.
// by renaming a verb or adding a required extension field) this
// test fails loudly.
func TestDHTWireCompatVanillaKRPC(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.IndexDir = t.TempDir()
	cfg.ListenPort = 0
	cfg.DisableDHT = false
	cfg.DisableDHTPublish = true // we only probe KRPC, no Layer-D traffic needed
	cfg.Seed = false
	cfg.NoUpload = true
	// Cut file-backed side state so the engine starts clean and
	// the test is hermetic.
	cfg.IdentityPath = ""
	cfg.ReputationPath = ""
	cfg.SeedListPath = ""
	cfg.BloomPath = ""
	cfg.TrustPath = ""
	cfg.PublisherManifest = ""
	cfg.CompanionDir = ""
	cfg.CompanionFollowFile = ""

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(context.Background(), cfg, log)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	engAddr := eng.DHTAddr()
	if engAddr == nil {
		t.Fatal("engine.DHTAddr() returned nil — DHT was not wired up")
	}
	// Engine's DHT listens on 0.0.0.0 — dial the same port on
	// 127.0.0.1 so the response can be routed back to the
	// vanilla server's loopback-bound socket without depending
	// on the kernel picking a non-loopback route.
	engUDP, ok := engAddr.(*net.UDPAddr)
	if !ok {
		t.Fatalf("DHTAddr type = %T, want *net.UDPAddr", engAddr)
	}
	target := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: engUDP.Port}

	// ------------------------------------------------------------
	// Build the vanilla client. NoSecurity lets us use a random
	// node ID regardless of our IP; Passive=true keeps it from
	// replying to queries (we're only sourcing traffic). No
	// StartingNodes/bootstrap configured — we drive addresses
	// manually.
	vanillaConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("listen vanilla udp: %v", err)
	}
	vanillaCfg := dht.NewDefaultServerConfig()
	vanillaCfg.Conn = vanillaConn
	vanillaCfg.NoSecurity = true
	vanillaCfg.Passive = true
	vanillaCfg.StartingNodes = func() ([]dht.Addr, error) { return nil, nil }
	// Deterministic node ID so log scrapers can find the test
	// traffic if it ever needs to be debugged.
	vanillaCfg.NodeId = krpc.IdFromString("swartznet-vanilla-kr")
	vanilla, err := dht.NewServer(vanillaCfg)
	if err != nil {
		t.Fatalf("dht.NewServer: %v", err)
	}
	t.Cleanup(vanilla.Close)

	dhtAddr := dht.NewAddr(target)
	rl := dht.QueryRateLimiting{}

	// ------------------------------------------------------------
	// 8.3-A: Ping.
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer pingCancel()
	pingQR := vanilla.Query(pingCtx, dhtAddr, "ping", dht.QueryInput{RateLimiting: rl})
	if pingQR.Err != nil {
		t.Fatalf("8.3-A ping: err=%v", pingQR.Err)
	}
	if pingQR.Reply.Y != "r" {
		t.Fatalf("8.3-A ping: reply Y=%q want \"r\"; full reply=%+v", pingQR.Reply.Y, pingQR.Reply)
	}
	if pingQR.Reply.R == nil {
		t.Fatal("8.3-A ping: reply.R nil")
	}
	var zeroID krpc.ID
	if pingQR.Reply.R.ID == zeroID {
		t.Error("8.3-A ping: reply.R.ID is zero")
	}

	// ------------------------------------------------------------
	// 8.3-B: get_peers. The infohash is synthetic — nothing in
	// the engine's peer store will match it, but BEP-5 requires
	// the node to still respond with at least a Token and a
	// closest-nodes list (possibly empty). The wire-compat claim
	// is the response shape, not the content.
	var ih krpc.ID
	for i := range ih {
		ih[i] = byte(i*11 + 1)
	}
	gpCtx, gpCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer gpCancel()
	gpQR := vanilla.GetPeers(gpCtx, dhtAddr, int160.FromByteArray(ih), false, rl)
	if gpQR.Err != nil {
		t.Fatalf("8.3-B get_peers: err=%v", gpQR.Err)
	}
	if gpQR.Reply.Y != "r" {
		t.Fatalf("8.3-B get_peers: reply Y=%q want \"r\"; full reply=%+v", gpQR.Reply.Y, gpQR.Reply)
	}
	if gpQR.Reply.R == nil {
		t.Fatal("8.3-B get_peers: reply.R nil")
	}
	if gpQR.Reply.R.Token == nil || *gpQR.Reply.R.Token == "" {
		t.Fatalf("8.3-B get_peers: reply.R.Token missing — reply=%+v R=%+v", gpQR.Reply, gpQR.Reply.R)
	}
	token := *gpQR.Reply.R.Token

	// ------------------------------------------------------------
	// 8.3-C: announce_peer. Uses the token the previous response
	// handed us. A successful reply is another r-type message
	// with the responding node's ID — anything else (including an
	// e-type error) is a wire-compat regression.
	apCtx, apCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer apCancel()
	port := 6881
	apArgs := krpc.MsgArgs{
		ID:       vanilla.ID(),
		InfoHash: ih,
		Port:     &port,
		Token:    token,
	}
	apQR := vanilla.Query(apCtx, dhtAddr, "announce_peer", dht.QueryInput{
		MsgArgs:      apArgs,
		RateLimiting: rl,
	})
	if apQR.Err != nil {
		t.Fatalf("8.3-C announce_peer: err=%v", apQR.Err)
	}
	if apQR.Reply.Y != "r" {
		t.Fatalf("8.3-C announce_peer: reply Y=%q want \"r\"; full reply=%+v", apQR.Reply.Y, apQR.Reply)
	}
	if apQR.Reply.R == nil {
		t.Fatal("8.3-C announce_peer: reply.R nil")
	}
}
