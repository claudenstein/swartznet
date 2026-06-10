package dhtindex_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/krpc"
	"golang.org/x/time/rate"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// newDeadNodeDHTServer builds a dht.Server whose only starting node
// is a UDP socket we own and never answer from. The put-side get
// traversal therefore completes with zero responses — the exact
// fail-open condition the checkPutStats guard exists for: getput.Put
// returns a nil error, but the BEP-44 item landed on zero nodes.
// QueryResendDelay is shrunk so each dead-node query gives up in
// tens of milliseconds instead of the 2-second production default.
func newDeadNodeDHTServer(t *testing.T) *dht.Server {
	t.Helper()
	deadConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("dead-node listen: %v", err)
	}
	t.Cleanup(func() { deadConn.Close() })
	deadAddr := deadConn.LocalAddr().(*net.UDPAddr)

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("server listen: %v", err)
	}
	cfg := dht.NewDefaultServerConfig()
	cfg.Conn = conn
	cfg.NoSecurity = true
	cfg.StartingNodes = func() ([]dht.Addr, error) {
		return []dht.Addr{dht.NewAddr(&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: deadAddr.Port})}, nil
	}
	cfg.QueryResendDelay = func() time.Duration { return 20 * time.Millisecond }
	// A private limiter: the fast resends against the dead node would
	// otherwise drain dht.DefaultSendLimiter (a package-level global,
	// 25 tokens/s) and starve the loopback round-trip tests running in
	// parallel into their own query timeouts.
	cfg.SendLimiter = rate.NewLimiter(rate.Inf, 0)
	cfg.NodeId = krpc.IdFromString("zeronode-put-test0001")
	srv, err := dht.NewServer(cfg)
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

// TestPutPathsFailClosedOnZeroNodes locks in the fail-closed
// zero-node guard on every AnacrolixPutter put path. Before the
// shared checkPutStats helper, PutInfohashPointer and PutPPMI
// discarded getput.Put's stats and returned nil when the item
// reached zero DHT nodes — the live companion caller then recorded
// success for a pointer no subscriber could resolve. All three
// paths must surface an error instead.
func TestPutPathsFailClosedOnZeroNodes(t *testing.T) {
	t.Parallel()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	srv := newDeadNodeDHTServer(t)
	putter, err := dhtindex.NewAnacrolixPutter(srv, priv)
	if err != nil {
		t.Fatalf("NewAnacrolixPutter: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cases := []struct {
		name string
		put  func() error
	}{
		{"pointer", func() error {
			var ih [20]byte
			ih[0] = 0xD7
			return putter.PutInfohashPointer(ctx, []byte("_sn_content_index_test"), ih)
		}},
		{"keyword", func() error {
			return putter.Put(ctx, []byte("ubuntu"), dhtindex.KeywordValue{
				Hits: []dhtindex.KeywordHit{
					{IH: []byte(strings.Repeat("\x01", 20)), N: "ubuntu"},
				},
			})
		}},
		{"ppmi", func() error {
			return putter.PutPPMI(ctx, dhtindex.PPMIValue{
				IH: []byte(strings.Repeat("\x02", 20)),
			})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.put()
			if err == nil {
				t.Fatal("put reached zero DHT nodes but returned nil (fail-open)")
			}
			if !strings.Contains(err.Error(), "zero DHT nodes") {
				t.Errorf("error = %q, want the zero-node guard message", err)
			}
		})
	}
}
