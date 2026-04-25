package dhtindex_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/krpc"
	"github.com/anacrolix/torrent/bencode"
	"github.com/swartznet/swartznet/internal/dhtindex"
	"github.com/swartznet/swartznet/internal/signing"
)

// startBep51Responder spins up a UDP loopback listener that
// replies to one sample_infohashes query with the given sample
// infohashes. Returns the bound addr and a done channel the test
// can <- to ensure the responder goroutine finished without
// error. Reused across CrawlOnce tests.
func startBep51Responder(t *testing.T, samples []krpc.ID) (net.PacketConn, <-chan struct{}) {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	// Serialise samples by hand because anacrolix's
	// CompactInfohashes.MarshalBinary is broken for non-empty
	// slices (see crawler_test.go for the full story).
	buf := make([]byte, 0, 20*len(samples))
	for _, s := range samples {
		buf = append(buf, s[:]...)
	}
	type rPart struct {
		ID      krpc.ID `bencode:"id"`
		Samples string  `bencode:"samples"`
	}
	type customReply struct {
		T string `bencode:"t"`
		Y string `bencode:"y"`
		R rPart  `bencode:"r"`
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		rbuf := make([]byte, 2048)
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, from, err := conn.ReadFrom(rbuf)
		if err != nil {
			t.Errorf("responder ReadFrom: %v", err)
			return
		}
		var q krpc.Msg
		if err := bencode.Unmarshal(rbuf[:n], &q); err != nil {
			t.Errorf("responder decode: %v", err)
			return
		}
		reply := customReply{
			T: q.T,
			Y: "r",
			R: rPart{
				ID:      krpc.ID{0x42},
				Samples: string(buf),
			},
		}
		out, err := bencode.Marshal(reply)
		if err != nil {
			t.Errorf("responder marshal: %v", err)
			return
		}
		if _, err := conn.WriteTo(out, from); err != nil {
			t.Errorf("responder WriteTo: %v", err)
		}
	}()
	return conn, done
}

// miniTorrentForIH builds a mini-torrent whose bencoded bytes
// can be used as a stub metainfo in CrawlOnce's fetch callback.
// The infohash is not deterministic from the caller's side —
// the test picks a sample key and associates it with the
// returned bytes.
func miniTorrentForIH(t *testing.T, kind string) []byte {
	t.Helper()
	mi := map[string]interface{}{
		"info": map[string]interface{}{
			"name":         "crawl-test-" + kind,
			"piece length": 16384,
			"pieces":       string(make([]byte, 20)),
			"length":       4,
		},
	}
	out, err := bencode.Marshal(mi)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return out
}

// TestCrawlOnceEndToEnd exercises the full pipeline:
//   - UDP responder serves one sample_infohashes reply with 4
//     distinct sample infohashes
//   - the fetcher returns, for each sample: a signed torrent,
//     a tampered-signed torrent, an unsigned torrent, and a
//     fetch error respectively
//   - CrawlOnce classifies all four and forwards 2 pubkeys to
//     the sink (the valid one and the tampered one with
//     sigValid=false)
func TestCrawlOnceEndToEnd(t *testing.T) {
	t.Parallel()

	sampleValid := krpc.ID{0x01}
	sampleBadSig := krpc.ID{0x02}
	sampleUnsigned := krpc.ID{0x03}
	sampleFetchErr := krpc.ID{0x04}

	respConn, done := startBep51Responder(t, []krpc.ID{
		sampleValid, sampleBadSig, sampleUnsigned, sampleFetchErr,
	})

	// Build per-sample metainfo fixtures.
	pubGood, privGood, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubBad, privBad, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	rawValid, err := signing.SignBytes(miniTorrentForIH(t, "valid"), privGood)
	if err != nil {
		t.Fatal(err)
	}
	rawBadSig, err := signing.SignBytes(miniTorrentForIH(t, "bad"), privBad)
	if err != nil {
		t.Fatal(err)
	}
	// Tamper — decode, mutate info.name, re-encode.
	{
		var mi map[string]bencode.Bytes
		if err := bencode.Unmarshal(rawBadSig, &mi); err != nil {
			t.Fatal(err)
		}
		var info map[string]interface{}
		if err := bencode.Unmarshal(mi["info"], &info); err != nil {
			t.Fatal(err)
		}
		info["name"] = "different"
		if mi["info"], err = bencode.Marshal(info); err != nil {
			t.Fatal(err)
		}
		if rawBadSig, err = bencode.Marshal(mi); err != nil {
			t.Fatal(err)
		}
	}
	rawUnsigned := miniTorrentForIH(t, "unsigned")

	fetch := func(ctx context.Context, ih krpc.ID) ([]byte, error) {
		switch ih {
		case sampleValid:
			return rawValid, nil
		case sampleBadSig:
			return rawBadSig, nil
		case sampleUnsigned:
			return rawUnsigned, nil
		case sampleFetchErr:
			return nil, errors.New("simulated fetch failure")
		}
		return nil, fmt.Errorf("unexpected IH %x", ih)
	}

	type sinkCall struct {
		pk       [32]byte
		sigValid bool
	}
	var forwarded []sinkCall
	sink := func(pk [32]byte, sigValid bool) {
		forwarded = append(forwarded, sinkCall{pk: pk, sigValid: sigValid})
	}

	srv := newIsolatedDHTServer(t)
	addr := dht.NewAddr(respConn.LocalAddr())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	outcome, err := dhtindex.CrawlOnce(ctx, srv, addr, krpc.ID{}, fetch, sink)
	if err != nil {
		t.Fatalf("CrawlOnce: %v", err)
	}
	<-done

	if outcome.Forwarded != 1 {
		t.Errorf("Forwarded = %d, want 1", outcome.Forwarded)
	}
	if outcome.BadSigs != 1 {
		t.Errorf("BadSigs = %d, want 1", outcome.BadSigs)
	}
	if outcome.Unsigned != 1 {
		t.Errorf("Unsigned = %d, want 1", outcome.Unsigned)
	}
	if outcome.FetchErrs != 1 {
		t.Errorf("FetchErrs = %d, want 1", outcome.FetchErrs)
	}
	if outcome.Malformed != 0 {
		t.Errorf("Malformed = %d, want 0", outcome.Malformed)
	}

	if len(forwarded) != 2 {
		t.Fatalf("sink received %d calls, want 2", len(forwarded))
	}
	// The good sigValid=true call should use pubGood's key.
	var gotGood, gotBad bool
	for _, c := range forwarded {
		if c.sigValid {
			gotGood = true
			if string(c.pk[:]) != string(pubGood) {
				t.Errorf("valid sink pubkey mismatch: got %x want %x", c.pk[:8], pubGood[:8])
			}
		} else {
			gotBad = true
			if string(c.pk[:]) != string(pubBad) {
				t.Errorf("bad-sig sink pubkey mismatch: got %x want %x", c.pk[:8], pubBad[:8])
			}
		}
	}
	if !gotGood || !gotBad {
		t.Errorf("missing sink calls: gotGood=%v gotBad=%v", gotGood, gotBad)
	}
}

// TestCrawlOnceNilGuards locks the nil-fetch / nil-sink
// validation paths. Both must error cleanly rather than
// panic deep inside the sample query.
func TestCrawlOnceNilGuards(t *testing.T) {
	t.Parallel()
	srv := newIsolatedDHTServer(t)
	addr := dht.NewAddr(&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1})

	if _, err := dhtindex.CrawlOnce(context.Background(), srv, addr, krpc.ID{}, nil, func([32]byte, bool) {}); err == nil {
		t.Error("expected error for nil fetch")
	}
	if _, err := dhtindex.CrawlOnce(context.Background(), srv, addr, krpc.ID{}, func(context.Context, krpc.ID) ([]byte, error) { return nil, nil }, nil); err == nil {
		t.Error("expected error for nil sink")
	}
}
