package dhtindex_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/krpc"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// TestAnacrolixInfohashPointerPutGetRoundTrip closes the
// production-path gap on PutInfohashPointer + GetInfohashPointer
// (BEP-46 companion pointer publishing). Two loopback dht.Servers
// each pointing at the other's address, then publish a 20-byte
// infohash via the publisher and fetch it back via the getter.
//
// Mirrors TestAnacrolixPPMIPutGetRoundTrip but exercises the
// pointer schema (bep46Pointer{IH}) instead of the PPMI value.
// Coverage gain: PutInfohashPointer and GetInfohashPointer both
// move from 20% (validation-only) → covering the real BEP-44
// traversal + sig path.
func TestAnacrolixInfohashPointerPutGetRoundTrip(t *testing.T) {
	t.Parallel()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub := priv.Public().(ed25519.PublicKey)
	var pubArr [32]byte
	copy(pubArr[:], pub)

	pubConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("publisher listen: %v", err)
	}
	getConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		pubConn.Close()
		t.Fatalf("getter listen: %v", err)
	}
	pubAddr := pubConn.LocalAddr().(*net.UDPAddr)
	getAddr := getConn.LocalAddr().(*net.UDPAddr)

	pubCfg := dht.NewDefaultServerConfig()
	pubCfg.Conn = pubConn
	pubCfg.NoSecurity = true
	pubCfg.StartingNodes = func() ([]dht.Addr, error) {
		return []dht.Addr{dht.NewAddr(&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: getAddr.Port})}, nil
	}
	pubCfg.NodeId = krpc.IdFromString("ptr-publisher-test001")
	pubSrv, err := dht.NewServer(pubCfg)
	if err != nil {
		t.Fatalf("publisher dht.NewServer: %v", err)
	}
	t.Cleanup(pubSrv.Close)

	getCfg := dht.NewDefaultServerConfig()
	getCfg.Conn = getConn
	getCfg.NoSecurity = true
	getCfg.StartingNodes = func() ([]dht.Addr, error) {
		return []dht.Addr{dht.NewAddr(&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: pubAddr.Port})}, nil
	}
	getCfg.NodeId = krpc.IdFromString("ptr-getter-test-00002")
	getSrv, err := dht.NewServer(getCfg)
	if err != nil {
		t.Fatalf("getter dht.NewServer: %v", err)
	}
	t.Cleanup(getSrv.Close)

	putter, err := dhtindex.NewAnacrolixPutter(pubSrv, priv)
	if err != nil {
		t.Fatalf("NewAnacrolixPutter: %v", err)
	}

	var ih [20]byte
	for i := range ih {
		ih[i] = 0xD7
	}
	salt := []byte("_sn_content_index_test")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := putter.PutInfohashPointer(ctx, salt, ih); err != nil {
		t.Fatalf("PutInfohashPointer: %v", err)
	}

	getter, err := dhtindex.NewAnacrolixGetter(getSrv)
	if err != nil {
		t.Fatalf("NewAnacrolixGetter: %v", err)
	}
	got, err := getter.GetInfohashPointer(ctx, pubArr, salt)
	if err != nil {
		t.Fatalf("GetInfohashPointer: %v", err)
	}
	if got != ih {
		t.Errorf("infohash mismatch: got %x, want %x", got, ih)
	}
}
