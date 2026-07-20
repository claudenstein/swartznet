package main

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
	"github.com/anacrolix/dht/v2/krpc"
	"golang.org/x/time/rate"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// TestStressFailedHard is the §6 fix in isolation: an all-failed stress phase is
// a HARD failure (fails the exit code); a partial or empty phase is not.
func TestStressFailedHard(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		sum  stressSummary
		want bool
	}{
		{"all failed", stressSummary{total: 5, ok: 0}, true},
		{"partial ok", stressSummary{total: 5, ok: 1}, false},
		{"all ok", stressSummary{total: 5, ok: 5}, false},
		{"no stress run", stressSummary{total: 0, ok: 0}, false},
	}
	for _, tc := range cases {
		if got := stressFailedHard(tc.sum); got != tc.want {
			t.Errorf("%s: stressFailedHard = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// newLoopbackServer builds a real loopback dht.Server bootstrapped against peers,
// mirroring the dhtindex cluster tests (NoSecurity + an unlimited SendLimiter so
// parallel tests don't drain the global limiter).
func newLoopbackServer(t *testing.T, nodeID string, peers []*net.UDPAddr) *dht.Server {
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

// TestSmokeRoundTripLoopback drives the tool's smoke() over a real 2-node
// loopback DHT: node A Puts a keyword value and Gets it back. A shared hub both
// nodes reach holds the item — the same shape as the Layer-D cluster gate.
func TestSmokeRoundTripLoopback(t *testing.T) {
	if testing.Short() {
		t.Skip("real-DHT loopback smoke skipped in -short")
	}
	t.Parallel()
	hub := newLoopbackServer(t, "dht-smoke-storage-hub00000000", nil)
	a := newLoopbackServer(t, "dht-smoke-node-a0000000000000", []*net.UDPAddr{udpAddr(hub)})

	// Let A learn the hub as a good node.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && a.Stats().GoodNodes < 1 {
		a.Bootstrap()
		time.Sleep(150 * time.Millisecond)
	}
	if a.Stats().GoodNodes < 1 {
		t.Skip("loopback A never saw a good node; environment blocks UDP")
	}

	pubKey, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var pub [32]byte
	copy(pub[:], pubKey)
	putter, err := dhtindex.NewAnacrolixPutter(a, priv)
	if err != nil {
		t.Fatal(err)
	}
	getter, err := dhtindex.NewAnacrolixGetter(a)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := smoke(ctx, log, putter, getter, pub); err != nil {
		t.Fatalf("smoke round-trip failed: %v", err)
	}
}
