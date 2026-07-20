package dhtindex_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/bep44"
	"github.com/anacrolix/dht/v2/krpc"
	"github.com/anacrolix/torrent/bencode"
	"golang.org/x/time/rate"

	"github.com/swartznet/swartznet/contracts/dhtschema"
	"github.com/swartznet/swartznet/internal/dhtindex"
)

// newLoopbackServer builds a real anacrolix dht.Server bound to 127.0.0.1,
// bootstrapped against the given peers, with NoSecurity (arbitrary node IDs on
// loopback) and a private SendLimiter so parallel tests don't drain the
// package-global limiter. exp overrides the BEP-44 item TTL (0 keeps the
// NewDefaultServerConfig 2h default; a negative-guard test passes a tiny value).
func newLoopbackServer(t *testing.T, nodeID string, peers []*net.UDPAddr, exp time.Duration) *dht.Server {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	cfg := dht.NewDefaultServerConfig()
	cfg.Conn = conn
	cfg.NoSecurity = true
	cfg.NodeId = krpc.IdFromString(nodeID)
	cfg.SendLimiter = rate.NewLimiter(rate.Inf, 0)
	cfg.QueryResendDelay = func() time.Duration { return 50 * time.Millisecond }
	if exp != 0 {
		cfg.Exp = exp
	}
	cfg.StartingNodes = func() ([]dht.Addr, error) {
		out := make([]dht.Addr, 0, len(peers))
		for _, p := range peers {
			out = append(out, dht.NewAddr(p))
		}
		return out, nil
	}
	srv, err := dht.NewServer(cfg)
	if err != nil {
		conn.Close()
		t.Fatalf("dht.NewServer: %v", err)
	}
	t.Cleanup(func() { srv.Close(); conn.Close() })
	return srv
}

func udpAddr(s *dht.Server) *net.UDPAddr {
	return &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: s.Addr().(*net.UDPAddr).Port}
}

// TestLayerDDHTClusterRoundTrip is the headline Layer-D gate: node A publishes
// a keyword value over a real BEP-44 put, node B (a distinct node that only
// learns A's pubkey via AddIndexerHex) resolves it via a real get and recovers
// A's infohash. A shared storage hub both nodes bootstrap against holds the
// item (mirroring how real DHT nodes share storage). Exp=2h on the hub (the
// NewDefaultServerConfig default) is what keeps the item alive between put and
// get — TestLayerDDHTClusterExpiresWithoutTTL is the negative guard.
func TestLayerDDHTClusterRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("real-DHT cluster test skipped in -short")
	}
	t.Parallel()

	hub := newLoopbackServer(t, "layerd-cluster-storage-hub0", nil, 0)
	a := newLoopbackServer(t, "layerd-cluster-node-a00000000", []*net.UDPAddr{udpAddr(hub)}, 0)
	b := newLoopbackServer(t, "layerd-cluster-node-b00000000", []*net.UDPAddr{udpAddr(hub)}, 0)

	// Warm routing tables so the traversals find the hub promptly.
	a.Ping(udpAddr(hub))
	b.Ping(udpAddr(hub))
	time.Sleep(500 * time.Millisecond)

	priv, pubArr := genClusterKey(t)
	putter, err := dhtindex.NewAnacrolixPutter(a, priv)
	if err != nil {
		t.Fatal(err)
	}
	var ih [20]byte
	for i := range ih {
		ih[i] = byte(i + 1)
	}
	salt, _ := dhtschema.SaltForKeyword("ubuntu")
	value := dhtschema.KeywordValue{Hits: []dhtschema.KeywordHit{{IH: ih[:], N: "Ubuntu 24.04", S: 42}}}

	putCtx, putCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer putCancel()
	if err := putter.Put(putCtx, salt, value); err != nil {
		t.Fatalf("node A publish failed: %v", err)
	}

	getter, err := dhtindex.NewAnacrolixGetter(b)
	if err != nil {
		t.Fatal(err)
	}
	// B knows only A's pubkey (as it would after peer_announce gossip / an
	// explicit AddIndexerHex), never the value itself.
	var got dhtschema.KeywordValue
	deadline := time.Now().Add(15 * time.Second)
	for attempt := 0; ; attempt++ {
		getCtx, getCancel := context.WithTimeout(context.Background(), 5*time.Second)
		got, err = getter.Get(getCtx, pubArr, salt)
		getCancel()
		if err == nil && len(got.Hits) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("node B never resolved A's keyword item (last err=%v, hits=%d)", err, len(got.Hits))
		}
		time.Sleep(300 * time.Millisecond)
	}
	if hex.EncodeToString(got.Hits[0].IH) != hex.EncodeToString(ih[:]) {
		t.Errorf("resolved infohash mismatch: got %x", got.Hits[0].IH)
	}
	if got.Hits[0].N != "Ubuntu 24.04" {
		t.Errorf("resolved name = %q", got.Hits[0].N)
	}
}

// TestLayerDDHTClusterExpiresWithoutTTL is the negative guard: with the storage
// hub's Exp shrunk to a tiny value, the item is expired by the time B reads, so
// the round-trip fails. Proves the Exp=2h pin is load-bearing.
func TestLayerDDHTClusterExpiresWithoutTTL(t *testing.T) {
	if testing.Short() {
		t.Skip("real-DHT cluster test skipped in -short")
	}
	t.Parallel()

	hub := newLoopbackServer(t, "layerd-expguard-storage-hub00", nil, time.Nanosecond)
	a := newLoopbackServer(t, "layerd-expguard-node-a0000000", []*net.UDPAddr{udpAddr(hub)}, 0)
	b := newLoopbackServer(t, "layerd-expguard-node-b0000000", []*net.UDPAddr{udpAddr(hub)}, 0)
	a.Ping(udpAddr(hub))
	b.Ping(udpAddr(hub))
	time.Sleep(500 * time.Millisecond)

	priv, pubArr := genClusterKey(t)
	putter, _ := dhtindex.NewAnacrolixPutter(a, priv)
	var ih [20]byte
	ih[0] = 0x7f
	salt, _ := dhtschema.SaltForKeyword("ubuntu")
	putCtx, putCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer putCancel()
	// The put may itself surface an error once the hub instantly expires the
	// item; either way, a subsequent get must not return the value.
	_ = putter.Put(putCtx, salt, dhtschema.KeywordValue{Hits: []dhtschema.KeywordHit{{IH: ih[:], N: "ubuntu"}}})
	time.Sleep(300 * time.Millisecond)

	getter, _ := dhtindex.NewAnacrolixGetter(b)
	getCtx, getCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer getCancel()
	got, err := getter.Get(getCtx, pubArr, salt)
	if err == nil && len(got.Hits) > 0 {
		t.Fatalf("expired item was resolved (Exp pin not load-bearing): %+v", got.Hits)
	}
}

// TestPutFailsClosedOnZeroNodes locks the fail-closed guard: a put whose only
// starting node never answers reaches zero DHT nodes, and every AnacrolixPutter
// path (keyword + BEP-46 pointer) must surface the "reached zero DHT nodes"
// error rather than record a false success.
func TestPutFailsClosedOnZeroNodes(t *testing.T) {
	if testing.Short() {
		t.Skip("real-DHT test skipped in -short")
	}
	t.Parallel()

	dead, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dead.Close() })
	deadAddr := dead.LocalAddr().(*net.UDPAddr)

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	cfg := dht.NewDefaultServerConfig()
	cfg.Conn = conn
	cfg.NoSecurity = true
	cfg.NodeId = krpc.IdFromString("layerd-zeronode-put-test0000")
	cfg.SendLimiter = rate.NewLimiter(rate.Inf, 0)
	cfg.QueryResendDelay = func() time.Duration { return 20 * time.Millisecond }
	cfg.StartingNodes = func() ([]dht.Addr, error) {
		return []dht.Addr{dht.NewAddr(&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: deadAddr.Port})}, nil
	}
	srv, err := dht.NewServer(cfg)
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close(); conn.Close() })

	priv, _ := genClusterKey(t)
	putter, _ := dhtindex.NewAnacrolixPutter(srv, priv)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	t.Run("keyword", func(t *testing.T) {
		err := putter.Put(ctx, []byte("ubuntu"), dhtschema.KeywordValue{
			Hits: []dhtschema.KeywordHit{{IH: []byte(strings.Repeat("\x01", 20)), N: "ubuntu"}},
		})
		if err == nil || !strings.Contains(err.Error(), "zero DHT nodes") {
			t.Fatalf("keyword put must fail closed, got %v", err)
		}
	})
	t.Run("pointer", func(t *testing.T) {
		var ih [20]byte
		ih[0] = 0xD7
		err := putter.PutInfohashPointer(ctx, []byte("_sn_content_index"), ih)
		if err == nil || !strings.Contains(err.Error(), "zero DHT nodes") {
			t.Fatalf("pointer put must fail closed, got %v", err)
		}
	})
	t.Run("ppmi", func(t *testing.T) {
		err := putter.PutPPMI(ctx, dhtschema.PPMIValue{IH: bytes.Repeat([]byte{1}, 20), Ts: 1700000000})
		if err == nil || !strings.Contains(err.Error(), "zero DHT nodes") {
			t.Fatalf("PPMI put must fail closed, got %v", err)
		}
	})
}

// TestVanillaBep44 closes wire-compat row 8.3-D: a client speaking only stock
// BEP-44 — no SwartzNet code — receives one of our keyword items, verifies its
// ed25519 signature, and parses the value as plain bencode. If we ever rename
// the bencode keys or exceed the 1000-byte cap this fails loudly.
func TestVanillaBep44(t *testing.T) {
	t.Parallel()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var ih1, ih2 [20]byte
	for i := range ih1 {
		ih1[i] = byte(i + 1)
		ih2[i] = byte(0xff - i)
	}
	value := dhtschema.KeywordValue{Hits: []dhtschema.KeywordHit{
		{IH: ih1[:], N: "ubuntu 24.04 desktop amd64", S: 128, F: 1, Sz: 6 << 30},
		{IH: ih2[:]},
	}}
	salt, _ := dhtschema.SaltForKeyword("ubuntu")
	encoded, err := dhtschema.EncodeValue(value)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 1000 {
		t.Fatalf("encoded value %d bytes exceeds BEP-44 cap 1000", len(encoded))
	}
	var v interface{}
	if err := bencode.Unmarshal(encoded, &v); err != nil {
		t.Fatal(err)
	}
	var pubArr [32]byte
	copy(pubArr[:], pub)
	const seq int64 = 1
	put := bep44.Put{V: v, K: &pubArr, Salt: salt, Seq: seq}
	put.Sign(priv)

	// (1) Stock signature verification — no SwartzNet knowledge.
	bv := bencode.MustMarshal(v)
	if !bep44.Verify(pub, salt, seq, bv, put.Sig[:]) {
		t.Fatal("stock bep44.Verify rejected our signed keyword item")
	}
	// (2) bep44.Check end-to-end (signature + caps).
	if err := bep44.Check(put.ToItem()); err != nil {
		t.Fatalf("stock bep44.Check failed: %v", err)
	}
	// (3) The value decodes into a plain map any bencode lib would see.
	var asMap map[string]any
	if err := bencode.Unmarshal(encoded, &asMap); err != nil {
		t.Fatalf("value did not decode as a plain map: %v", err)
	}
	hits, ok := asMap["hits"].([]any)
	if !ok || len(hits) != 2 {
		t.Fatalf("hits field not a 2-entry list: %T", asMap["hits"])
	}
	if _, ok := asMap["ts"]; !ok {
		t.Error("value missing the ts field a vanilla client documents")
	}
}

func genClusterKey(t *testing.T) (ed25519.PrivateKey, [32]byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var arr [32]byte
	copy(arr[:], pub)
	return priv, arr
}

// TestPPMIClusterRoundTrip: node A publishes a PPMI item over a real BEP-44 put
// at SHA1(pubkey||PPMISalt); node B resolves it via GetPPMI and recovers A's
// merged-index infohash + commit. Same shared-hub topology as the keyword test.
func TestPPMIClusterRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("real-DHT cluster test skipped in -short")
	}
	t.Parallel()

	hub := newLoopbackServer(t, "ppmi-cluster-storage-hub00000", nil, 0)
	a := newLoopbackServer(t, "ppmi-cluster-node-a0000000000", []*net.UDPAddr{udpAddr(hub)}, 0)
	b := newLoopbackServer(t, "ppmi-cluster-node-b0000000000", []*net.UDPAddr{udpAddr(hub)}, 0)
	a.Ping(udpAddr(hub))
	b.Ping(udpAddr(hub))
	time.Sleep(500 * time.Millisecond)

	priv, pubArr := genClusterKey(t)
	putter, err := dhtindex.NewAnacrolixPutter(a, priv)
	if err != nil {
		t.Fatal(err)
	}
	var ih [20]byte
	for i := range ih {
		ih[i] = byte(0x30 + i)
	}
	commit := bytes.Repeat([]byte{0xcc}, 32)
	putCtx, putCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer putCancel()
	if err := putter.PutPPMI(putCtx, dhtschema.PPMIValue{IH: ih[:], Commit: commit, Ts: 1700000000}); err != nil {
		t.Fatalf("node A PutPPMI failed: %v", err)
	}

	getter, err := dhtindex.NewAnacrolixGetter(b)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	var got dhtschema.PPMIValue
	for {
		getCtx, getCancel := context.WithTimeout(context.Background(), 5*time.Second)
		got, err = getter.GetPPMI(getCtx, pubArr)
		getCancel()
		if err == nil && len(got.IH) == 20 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("node B never resolved A's PPMI (last err=%v)", err)
		}
		time.Sleep(300 * time.Millisecond)
	}
	if hex.EncodeToString(got.IH) != hex.EncodeToString(ih[:]) {
		t.Errorf("resolved PPMI infohash mismatch: got %x", got.IH)
	}
	if hex.EncodeToString(got.Commit) != hex.EncodeToString(commit) {
		t.Errorf("resolved PPMI commit mismatch")
	}
}
