package engine_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/bep44"
	"github.com/anacrolix/dht/v2/exts/getput"
	"github.com/anacrolix/dht/v2/krpc"
	"github.com/anacrolix/torrent/bencode"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// TestDHTEnginePutReceive is the regression gate for the s12
// BEP-44 "put succeeds, immediate get returns value-not-found"
// bug. A raw anacrolix dht.Server (probe) issues a BEP-44 put
// against an engine-hosted DHT server over loopback; the probe
// then reads the item back with getput.Get.
//
// This MUST pass. The root cause, pinned by this iteration of
// the harness, was that anacrolix/torrent's NewAnacrolixDhtServer
// builds a dht.ServerConfig from scratch and does NOT carry
// dht.NewDefaultServerConfig's Exp=2h default. Left at 0, the
// bep44.Wrapper treats every stored item as instantly expired
// (`i.created.Add(0).After(now)` = false), so the next get
// deletes the item and returns ErrItemNotFound. Fixed in
// engine.go by pinning sc.Exp = 2*time.Hour when the upstream
// didn't set one.
//
// If this test starts failing again, check whether sc.Exp is
// still being set in engine.New's ConfigureAnacrolixDhtServer
// callback.
func TestDHTEnginePutReceive(t *testing.T) {
	t.Parallel()

	// --- 1. Start an engine with DHT enabled, bound to loopback.
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.IndexDir = t.TempDir()
	cfg.IdentityPath = ""
	cfg.ReputationPath = ""
	cfg.SeedListPath = ""
	cfg.BloomPath = ""
	cfg.TrustPath = ""
	cfg.PublisherManifest = ""
	cfg.CompanionDir = ""
	cfg.CompanionFollowFile = ""
	cfg.ListenPort = 0
	cfg.DisableDHT = false
	cfg.DisableDHTPublish = true
	cfg.DHTInsecure = true
	cfg.ListenHost = "127.0.0.1"
	cfg.DisableIPv6 = true
	// Dead-end bootstrap address so the engine's DHT doesn't
	// fall through to anacrolix's public-mainline default
	// (router.bittorrent.com et al.). Leaks to the public DHT
	// would poison the test's routing-table state with nodes
	// that happen to reply from the real internet.
	cfg.DHTBootstrapAddrs = []string{"127.0.0.1:1"}
	cfg.Seed = false
	cfg.NoUpload = true

	eng, err := engine.New(context.Background(), cfg, log)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	engAddr, ok := eng.DHTAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("engine DHTAddr type=%T", eng.DHTAddr())
	}
	// The engine's DHT is bound to 127.0.0.1 (via cfg.ListenHost).
	// Use that port directly on 127.0.0.1 so loopback routing is
	// unambiguous.
	engTarget := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: engAddr.Port}

	// --- 2. Start a raw anacrolix dht.Server as the probe-sender.
	probeConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	probeCfg := dht.NewDefaultServerConfig()
	probeCfg.Conn = probeConn
	probeCfg.NoSecurity = true
	probeCfg.StartingNodes = func() ([]dht.Addr, error) {
		return []dht.Addr{dht.NewAddr(engTarget)}, nil
	}
	probeCfg.NodeId = krpc.IdFromString("engine-put-receive-probe")
	probe, err := dht.NewServer(probeCfg)
	if err != nil {
		t.Fatalf("probe NewServer: %v", err)
	}
	t.Cleanup(probe.Close)

	// Give the DHTs a beat to ping and populate each other's
	// routing tables.
	time.Sleep(1 * time.Second)
	good, total := eng.DHTRoutingTableSize()
	t.Logf("engine routing: good=%d total=%d", good, total)

	// --- 3. Issue a BEP-44 put from the raw probe. This goes over
	//    the wire to the engine's DHT, which runs its store.Put
	//    (bep44.Wrapper.Put → Check → Memory.Put).
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub := priv.Public().(ed25519.PublicKey)
	var pubArr [32]byte
	copy(pubArr[:], pub)

	salt := []byte("put-receive-probe")
	value := map[string]any{"greeting": "hello-from-probe"}

	put := &bep44.Put{
		V:    value,
		K:    &pubArr,
		Salt: salt,
		Seq:  1,
	}
	put.Sign(priv)

	putCtx, putCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer putCancel()
	putStats, err := getput.Put(putCtx, put.Target(), probe, salt, func(seq int64) bep44.Put {
		// getput.Put calls this with the seq it discovered via
		// get traversal. Increment past that so our put wins.
		p := *put
		p.Seq = seq + 1
		p.Sign(priv)
		return p
	})
	if err != nil {
		t.Fatalf("probe put failed: %v", err)
	}
	t.Logf("probe put stats: NumAddrsTried=%d NumResponses=%d",
		putStats.NumAddrsTried, putStats.NumResponses)

	// --- 4. Read the item back from the probe. Traversal
	//    visits the engine (the only known peer), which should
	//    serve the item from its local store. If the engine
	//    silently rejected the put in its Wrapper, this get
	//    returns "value not found".
	time.Sleep(500 * time.Millisecond)

	getCtx, getCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer getCancel()
	result, getStats, err := getput.Get(getCtx, put.Target(), probe, nil, salt)
	if err != nil {
		t.Fatalf("probe get failed — the engine's DHT did not store our put: %v "+
			"(get stats: NumAddrsTried=%d NumResponses=%d)",
			err, getStats.NumAddrsTried, getStats.NumResponses)
	}

	// --- 5. Verify the retrieved value is what we put.
	vBytes, err := bencode.Marshal(result.V)
	if err != nil {
		t.Fatalf("remarshal V: %v", err)
	}
	var got map[string]any
	if err := bencode.Unmarshal(vBytes, &got); err != nil {
		t.Fatalf("decode V: %v", err)
	}
	if got["greeting"] != "hello-from-probe" {
		t.Errorf("got greeting=%v want hello-from-probe; full V=%v", got["greeting"], got)
	}
	t.Logf("round-trip succeeded: V=%v seq=%d", got, result.Seq)
}
