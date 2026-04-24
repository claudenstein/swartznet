package dhtindex_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/krpc"
	"github.com/anacrolix/torrent/bencode"
	"github.com/swartznet/swartznet/internal/dhtindex"
)

// TestSampleInfohashesNilGuards covers the two nil-parameter
// guards — they return errors rather than panic so callers
// that accidentally pass a half-constructed server or addr get
// a clear failure.
func TestSampleInfohashesNilGuards(t *testing.T) {
	t.Parallel()
	var target krpc.ID

	if _, err := dhtindex.SampleInfohashes(context.Background(), nil, dht.NewAddr(&net.UDPAddr{}), target); err == nil {
		t.Error("expected error for nil server")
	}

	srv := newIsolatedDHTServer(t)
	if _, err := dhtindex.SampleInfohashes(context.Background(), srv, nil, target); err == nil {
		t.Error("expected error for nil addr")
	}
}

// TestSampleInfohashesParsesResponse spins up a hand-rolled UDP
// responder that returns a real BEP-51 response (samples +
// interval + num + nodes), then verifies SampleInfohashes
// unmarshals every field correctly. Tests the happy path without
// requiring anacrolix to natively handle sample_infohashes.
func TestSampleInfohashesParsesResponse(t *testing.T) {
	t.Parallel()

	// Responder listens on loopback, reads one packet, sends back
	// a crafted reply. No real DHT — just enough wire to satisfy
	// anacrolix's Server.Query transport.
	respConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket: %v", err)
	}
	defer respConn.Close()

	sampleA := krpc.ID{0x01, 0x02, 0x03}
	sampleB := krpc.ID{0xAA, 0xBB, 0xCC}
	interval := int64(60)
	num := int64(123456)
	nodeA := krpc.NodeInfo{
		ID:   krpc.ID{0x11},
		Addr: krpc.NodeAddr{IP: net.IPv4(10, 0, 0, 1).To4(), Port: 4242},
	}

	// Custom reply shape — anacrolix's CompactInfohashes
	// MarshalBinary panics for non-empty slices, so we carry the
	// concatenated 20-byte samples as a raw string field and
	// encode it ourselves.
	type rPart struct {
		ID       krpc.ID                  `bencode:"id"`
		Nodes    krpc.CompactIPv4NodeInfo `bencode:"nodes,omitempty"`
		Samples  string                   `bencode:"samples"`
		Interval int64                    `bencode:"interval"`
		Num      int64                    `bencode:"num"`
	}
	type customReply struct {
		T string `bencode:"t"`
		Y string `bencode:"y"`
		R rPart  `bencode:"r"`
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 2048)
		_ = respConn.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, from, err := respConn.ReadFrom(buf)
		if err != nil {
			t.Errorf("responder ReadFrom: %v", err)
			return
		}
		var q krpc.Msg
		if err := bencode.Unmarshal(buf[:n], &q); err != nil {
			t.Errorf("responder decode: %v", err)
			return
		}
		if q.Q != "sample_infohashes" {
			t.Errorf("responder q = %q, want sample_infohashes", q.Q)
		}
		samples := make([]byte, 0, 40)
		samples = append(samples, sampleA[:]...)
		samples = append(samples, sampleB[:]...)
		reply := customReply{
			T: q.T,
			Y: "r",
			R: rPart{
				ID:       krpc.ID{0x42},
				Nodes:    krpc.CompactIPv4NodeInfo{nodeA},
				Samples:  string(samples),
				Interval: interval,
				Num:      num,
			},
		}
		out, err := bencode.Marshal(reply)
		if err != nil {
			t.Errorf("responder marshal: %v", err)
			return
		}
		if _, err := respConn.WriteTo(out, from); err != nil {
			t.Errorf("responder WriteTo: %v", err)
		}
	}()

	srv := newIsolatedDHTServer(t)
	addr := dht.NewAddr(respConn.LocalAddr())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	got, err := dhtindex.SampleInfohashes(ctx, srv, addr, krpc.ID{})
	if err != nil {
		t.Fatalf("SampleInfohashes: %v", err)
	}

	// Wait for responder goroutine to finish so we don't race on
	// its t.Errorf calls.
	<-done

	if len(got.Samples) != 2 {
		t.Fatalf("Samples len = %d, want 2", len(got.Samples))
	}
	if got.Samples[0] != sampleA {
		t.Errorf("Samples[0] = %x, want %x", got.Samples[0], sampleA)
	}
	if got.Samples[1] != sampleB {
		t.Errorf("Samples[1] = %x, want %x", got.Samples[1], sampleB)
	}
	if got.Interval != interval {
		t.Errorf("Interval = %d, want %d", got.Interval, interval)
	}
	if got.Num != num {
		t.Errorf("Num = %d, want %d", got.Num, num)
	}
	if len(got.Nodes) != 1 {
		t.Errorf("Nodes len = %d, want 1", len(got.Nodes))
	} else if got.Nodes[0].ID != nodeA.ID {
		t.Errorf("Nodes[0].ID = %x, want %x", got.Nodes[0].ID, nodeA.ID)
	}
}
