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

// TestAnacrolixPPMIPutGetRoundTrip closes the production-path
// gap on PutPPMI and GetPPMI. Spins two loopback dht.Servers
// each pointing at the other's address, publishes a PPMI via
// AnacrolixPutter, fetches it back via AnacrolixGetter, and
// verifies the round-trip preserves IH + Commit + Topics + Ts.
//
// Mirrors the architecture of TestVanillaBEP44GetterReadsOurItem
// but exercises the PPMI salt + value schema instead of the
// legacy keyword schema. Coverage gain: anacrolix-side
// PutPPMI/GetPPMI both move from 0% → 100%.
func TestAnacrolixPPMIPutGetRoundTrip(t *testing.T) {
	t.Parallel()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub := priv.Public().(ed25519.PublicKey)
	var pubArr [32]byte
	copy(pubArr[:], pub)

	// Pre-allocate both UDP sockets so each server's
	// StartingNodes can point at the other.
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
	pubCfg.NodeId = krpc.IdFromString("ppmi-publisher-test1")
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
	getCfg.NodeId = krpc.IdFromString("ppmi-getter-test-002")
	getSrv, err := dht.NewServer(getCfg)
	if err != nil {
		t.Fatalf("getter dht.NewServer: %v", err)
	}
	t.Cleanup(getSrv.Close)

	// Publish via PutPPMI.
	putter, err := dhtindex.NewAnacrolixPutter(pubSrv, priv)
	if err != nil {
		t.Fatalf("NewAnacrolixPutter: %v", err)
	}

	ih := make([]byte, 20)
	for i := range ih {
		ih[i] = 0xC1
	}
	commit := make([]byte, 32)
	for i := range commit {
		commit[i] = 0xC2
	}
	topics := make([]byte, 32)
	for i := range topics {
		topics[i] = 0xC3
	}
	want := dhtindex.PPMIValue{
		IH:     ih,
		Commit: commit,
		Topics: topics,
		Ts:     0, // PutPPMI must fill from clock when zero
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := putter.PutPPMI(ctx, want); err != nil {
		t.Fatalf("PutPPMI: %v", err)
	}

	// Fetch via GetPPMI.
	getter, err := dhtindex.NewAnacrolixGetter(getSrv)
	if err != nil {
		t.Fatalf("NewAnacrolixGetter: %v", err)
	}
	got, err := getter.GetPPMI(ctx, pubArr)
	if err != nil {
		t.Fatalf("GetPPMI: %v", err)
	}

	if string(got.IH) != string(ih) {
		t.Errorf("IH = %x, want %x", got.IH, ih)
	}
	if string(got.Commit) != string(commit) {
		t.Errorf("Commit = %x, want %x", got.Commit, commit)
	}
	if string(got.Topics) != string(topics) {
		t.Errorf("Topics = %x, want %x", got.Topics, topics)
	}
	if got.Ts == 0 {
		t.Error("Ts should have been filled from clock; got 0")
	}
}
