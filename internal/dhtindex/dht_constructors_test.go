package dhtindex_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"

	"github.com/anacrolix/dht/v2"
	"github.com/swartznet/swartznet/internal/dhtindex"
)

// newIsolatedDHTServer wires a dht.Server to a UDP socket on
// localhost:0 with no bootstrap nodes. The server never sees
// real traffic and is torn down via t.Cleanup.
func newIsolatedDHTServer(t *testing.T) *dht.Server {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket: %v", err)
	}
	srv, err := dht.NewServer(&dht.ServerConfig{
		Conn:       conn,
		NoSecurity: true,
		Passive:    true, // don't respond to queries
	})
	if err != nil {
		conn.Close()
		t.Fatalf("dht.NewServer: %v", err)
	}
	t.Cleanup(func() {
		srv.Close()
		conn.Close()
	})
	return srv
}

// TestNewAnacrolixPutterSuccessAndPublicKey covers the success
// branch of NewAnacrolixPutter and the PublicKey getter. Both
// were 0%-covered because every existing test passed nil for
// the server (which short-circuits on the first guard).
func TestNewAnacrolixPutterSuccessAndPublicKey(t *testing.T) {
	t.Parallel()
	srv := newIsolatedDHTServer(t)

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	put, err := dhtindex.NewAnacrolixPutter(srv, priv)
	if err != nil {
		t.Fatalf("NewAnacrolixPutter: %v", err)
	}
	if put == nil {
		t.Fatal("nil putter")
	}

	got := put.PublicKey()
	if string(got[:]) != string(pub) {
		t.Errorf("PublicKey mismatch: got %x, want %x", got[:8], pub[:8])
	}
}

// TestNewAnacrolixGetterSuccess covers the success branch of
// NewAnacrolixGetter — same constraint as the putter test:
// existing nil-server tests short-circuit on the first guard.
func TestNewAnacrolixGetterSuccess(t *testing.T) {
	t.Parallel()
	srv := newIsolatedDHTServer(t)

	get, err := dhtindex.NewAnacrolixGetter(srv)
	if err != nil {
		t.Fatalf("NewAnacrolixGetter: %v", err)
	}
	if get == nil {
		t.Fatal("nil getter")
	}
}
