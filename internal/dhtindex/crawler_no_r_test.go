package dhtindex_test

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/krpc"
	"github.com/anacrolix/torrent/bencode"
	"github.com/swartznet/swartznet/internal/dhtindex"
)

// TestSampleInfohashesNoRDict covers SampleInfohashes' `if r == nil`
// arm at crawler.go:59-61. Responder replies with y="r" (so
// ToError returns nil) but no r dict, so the r-pointer SampleInfohashes
// dereferences is nil and the wrapped "no r dict" error must surface.
func TestSampleInfohashesNoRDict(t *testing.T) {
	t.Parallel()

	respConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket: %v", err)
	}
	defer respConn.Close()

	// Reply shape: y="r" but no "r" key. ToError will return nil
	// because no "e" field is present, so we land on the r==nil arm.
	type reply struct {
		T string `bencode:"t"`
		Y string `bencode:"y"`
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
		out, err := bencode.Marshal(reply{T: q.T, Y: "r"})
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

	_, err = dhtindex.SampleInfohashes(ctx, srv, addr, krpc.ID{})
	<-done
	if err == nil {
		t.Fatal("expected error for reply with no r dict")
	}
	if !strings.Contains(err.Error(), "no r dict") {
		t.Errorf("err = %q, want it to mention 'no r dict'", err.Error())
	}
}
