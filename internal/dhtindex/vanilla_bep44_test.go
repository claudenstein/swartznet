package dhtindex_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"net"
	"testing"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/bep44"
	"github.com/anacrolix/dht/v2/exts/getput"
	"github.com/anacrolix/dht/v2/krpc"
	"github.com/anacrolix/torrent/bencode"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// TestVanillaBEP44GetterReadsOurItem closes wire-compat matrix row 8.3-D:
// a plain anacrolix dht.Server ("vanilla BEP-44 getter") issues a BEP-44
// mutable-item get for a keyword entry published by the SwartzNet
// AnacrolixPutter. The retrieved value must decode as a valid KeywordValue
// with the correct hits, and the BEP-44 signature must pass.
//
// This is the most important single test in the project for the Layer-D
// design claim: "our items look like any other BEP-44 item." If this test
// fails, the whole BEP-44 design is broken.
//
// Setup (two loopback UDP servers, each bootstrapped to the other):
//  1. A "publisher" dht.Server publishes a keyword entry via AnacrolixPutter.
//     The traversal stores the item on the "vanilla" server (the closest
//     known node to the BEP-44 target, since it's the only peer).
//  2. A "vanilla" dht.Server issues getput.Get for the same target.
//     The traversal finds the item on the publisher server (the only peer
//     in the vanilla server's routing table). getput.Get verifies the
//     BEP-44 ed25519 signature internally before returning the value.
//  3. We decode the returned V bytes as a KeywordValue and check hits.
func TestVanillaBEP44GetterReadsOurItem(t *testing.T) {
	t.Parallel()

	// ----------------------------------------------------------------
	// 1. Generate a publisher ed25519 keypair.
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub := priv.Public().(ed25519.PublicKey)
	var pubArr [32]byte
	copy(pubArr[:], pub)

	// ----------------------------------------------------------------
	// 2. Pre-allocate both UDP sockets so each server knows the other's
	//    address before starting. We need this to wire their StartingNodes
	//    functions so traversals find each other.
	pubConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("publisher listen: %v", err)
	}
	vanillaConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		pubConn.Close()
		t.Fatalf("vanilla listen: %v", err)
	}

	pubAddr := pubConn.LocalAddr().(*net.UDPAddr)
	vanillaAddr := vanillaConn.LocalAddr().(*net.UDPAddr)

	// ----------------------------------------------------------------
	// 3. Start the publisher DHT server. Its StartingNodes point at the
	//    vanilla server so getput.Put has a peer to store the item with.
	pubSrvCfg := dht.NewDefaultServerConfig()
	pubSrvCfg.Conn = pubConn
	pubSrvCfg.NoSecurity = true
	pubSrvCfg.StartingNodes = func() ([]dht.Addr, error) {
		return []dht.Addr{dht.NewAddr(&net.UDPAddr{
			IP:   net.ParseIP("127.0.0.1"),
			Port: vanillaAddr.Port,
		})}, nil
	}
	pubSrvCfg.NodeId = krpc.IdFromString("swartznet-pub83D-000")
	pubSrv, err := dht.NewServer(pubSrvCfg)
	if err != nil {
		t.Fatalf("publisher dht.NewServer: %v", err)
	}
	t.Cleanup(pubSrv.Close)

	// ----------------------------------------------------------------
	// 4. Start the vanilla DHT server. Its StartingNodes point at the
	//    publisher so getput.Get has a peer to start the traversal from.
	vanillaCfg := dht.NewDefaultServerConfig()
	vanillaCfg.Conn = vanillaConn
	vanillaCfg.NoSecurity = true
	vanillaCfg.StartingNodes = func() ([]dht.Addr, error) {
		return []dht.Addr{dht.NewAddr(&net.UDPAddr{
			IP:   net.ParseIP("127.0.0.1"),
			Port: pubAddr.Port,
		})}, nil
	}
	vanillaCfg.NodeId = krpc.IdFromString("vanilla-getter-8.3-D")
	vanillaSrv, err := dht.NewServer(vanillaCfg)
	if err != nil {
		t.Fatalf("vanilla dht.NewServer: %v", err)
	}
	t.Cleanup(vanillaSrv.Close)

	// ----------------------------------------------------------------
	// 5. Publish via AnacrolixPutter. The publisher's traversal queries
	//    the vanilla server for the closest nodes, receives itself, and
	//    stores the item on the vanilla server.
	putter, err := dhtindex.NewAnacrolixPutter(pubSrv, priv)
	if err != nil {
		t.Fatalf("NewAnacrolixPutter: %v", err)
	}

	var ih [20]byte
	for i := range ih {
		ih[i] = 0xbb
	}
	keyword := "photon"
	hit := dhtindex.KeywordHit{
		IH: ih[:],
		N:  "photon-test-torrent",
		S:  7,
	}
	value := dhtindex.KeywordValue{Hits: []dhtindex.KeywordHit{hit}}
	salt, err := dhtindex.SaltForKeyword(keyword)
	if err != nil {
		t.Fatalf("SaltForKeyword: %v", err)
	}

	putCtx, putCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer putCancel()
	if err := putter.Put(putCtx, salt, value); err != nil {
		t.Fatalf("8.3-D: AnacrolixPutter.Put failed: %v", err)
	}
	t.Logf("8.3-D: published %q → ih=%s", keyword, hex.EncodeToString(ih[:]))

	// ----------------------------------------------------------------
	// 6. Vanilla server issues a BEP-44 get for the published target.
	//    No SwartzNet code involved on this side. getput.Get verifies
	//    the ed25519 signature internally — if the item were forged or
	//    our signing format deviated from BEP-44, this would return an
	//    error or an empty result.
	target := bep44.MakeMutableTarget(pubArr, salt)

	getCtx, getCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer getCancel()
	result, _, err := getput.Get(getCtx, target, vanillaSrv, nil, salt)
	if err != nil {
		t.Fatalf("8.3-D: vanilla getput.Get failed: %v", err)
	}
	if !result.Mutable {
		t.Fatal("8.3-D: vanilla got an immutable item; expected mutable (signed BEP-44)")
	}
	t.Logf("8.3-D: vanilla got item seq=%d", result.Seq)

	// ----------------------------------------------------------------
	// 7. Decode the returned V bytes as a KeywordValue and assert hits.
	//    V is stored as an interface{} by anacrolix; we re-encode it
	//    to bytes and then use dhtindex.DecodeValue.
	vBytes, err := bencode.Marshal(result.V)
	if err != nil {
		t.Fatalf("8.3-D: re-marshal V: %v", err)
	}
	got, err := dhtindex.DecodeValue(vBytes)
	if err != nil {
		t.Fatalf("8.3-D: DecodeValue: %v", err)
	}
	if len(got.Hits) != 1 {
		t.Fatalf("8.3-D: len(Hits) = %d, want 1", len(got.Hits))
	}
	gotHit := got.Hits[0]
	if hex.EncodeToString(gotHit.IH) != hex.EncodeToString(ih[:]) {
		t.Errorf("8.3-D: hit IH = %x, want %x", gotHit.IH, ih)
	}
	if gotHit.N != hit.N {
		t.Errorf("8.3-D: hit N = %q, want %q", gotHit.N, hit.N)
	}
	if gotHit.S != hit.S {
		t.Errorf("8.3-D: hit S = %d, want %d", gotHit.S, hit.S)
	}
	t.Logf("8.3-D: PASS — vanilla BEP-44 getter read ih=%x name=%q seeders=%d",
		gotHit.IH, gotHit.N, gotHit.S)
}
