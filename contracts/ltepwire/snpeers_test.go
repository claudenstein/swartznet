package ltepwire

import (
	"bytes"
	"testing"

	"github.com/anacrolix/torrent/bencode"
)

func v4(a, b, c, d byte, port uint16) CompactAddr {
	return CompactAddr{IP: []byte{a, b, c, d}, Port: port}
}

func TestSnPeersRoundTrip(t *testing.T) {
	in := []CompactAddr{
		v4(1, 2, 3, 4, 6881),
		v4(10, 0, 0, 5, 51413),
		{IP: bytes.Repeat([]byte{0x20}, 16), Port: 6969}, // a v6 endpoint
	}
	frame, err := EncodeSnPeers(in)
	if err != nil {
		t.Fatal(err)
	}
	// It IS an sn_peers frame.
	if mt, err := PeekMsgType(frame); err != nil || mt != MsgTypeSnPeers {
		t.Fatalf("PeekMsgType = %d,%v want %d", mt, err, MsgTypeSnPeers)
	}
	out, err := DecodeSnPeers(frame)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("decoded %d addrs, want 3: %+v", len(out), out)
	}
	for i := range in {
		if !bytes.Equal(out[i].IP, in[i].IP) || out[i].Port != in[i].Port {
			t.Errorf("addr[%d] = %+v, want %+v", i, out[i], in[i])
		}
	}
}

func TestSnPeersEncodeRejectsBadInput(t *testing.T) {
	// IP that is neither 4 nor 16 bytes.
	if _, err := EncodeSnPeers([]CompactAddr{{IP: []byte{1, 2, 3}, Port: 1}}); err == nil {
		t.Error("bad-length IP should error")
	}
	// Over the cap.
	big := make([]CompactAddr, MaxPeersPerGossip+1)
	for i := range big {
		big[i] = v4(1, 1, 1, byte(i), 1)
	}
	if _, err := EncodeSnPeers(big); err == nil {
		t.Error("over-cap encode should error")
	}
}

func TestSnPeersDecodeTolerant(t *testing.T) {
	// Hand-build a frame with trailing partial bytes + a zero-port record + a
	// zero-IP record, and confirm decode drops exactly those.
	good := v4(8, 8, 8, 8, 53)
	var v4blob []byte
	v4blob = append(v4blob, good.IP...)
	v4blob = append(v4blob, byte(good.Port>>8), byte(good.Port))
	v4blob = append(v4blob, 9, 9, 9, 9, 0, 0)       // zero port → dropped
	v4blob = append(v4blob, 0, 0, 0, 0, 0x1a, 0x0b) // zero IP → dropped
	v4blob = append(v4blob, 1, 2, 3)                // trailing partial → ignored

	// Marshal a SnPeers with our raw (partly malformed) blob directly.
	raw, err := marshalSnPeersRaw(v4blob, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := DecodeSnPeers(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || !bytes.Equal(out[0].IP, good.IP) || out[0].Port != good.Port {
		t.Fatalf("tolerant decode = %+v, want just %+v", out, good)
	}
}

func TestSnPeersDecodeCapTruncates(t *testing.T) {
	// A blob declaring far more than the cap decodes to at most MaxPeersPerGossip.
	var blob []byte
	for i := 0; i < MaxPeersPerGossip*3; i++ {
		blob = append(blob, 1, 1, 1, byte(i%250)+1, 0x1a, 0x0b)
	}
	raw, err := marshalSnPeersRaw(blob, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := DecodeSnPeers(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > MaxPeersPerGossip {
		t.Errorf("decoded %d addrs, want <= %d", len(out), MaxPeersPerGossip)
	}
}

func TestSnPeersWrongType(t *testing.T) {
	pa, _ := EncodePeerAnnounce(PeerAnnounce{})
	if _, err := DecodeSnPeers(pa); err == nil {
		t.Error("decoding a peer_announce as sn_peers should error")
	}
}

func TestPeerGossipBit(t *testing.T) {
	if Announced(Sharing{}, RuntimeFacts{}) != 0 {
		t.Error("empty facts should announce no bits")
	}
	m := ServiceBits(Announced(Sharing{}, RuntimeFacts{PeerGossip: true}))
	if !m.Has(BitPeerGossip) {
		t.Errorf("PeerGossip fact did not set bit 10 (0x400): mask=%#x", uint64(m))
	}
	if BitPeerGossip != 1<<10 {
		t.Errorf("BitPeerGossip = %#x, want 0x400", uint64(BitPeerGossip))
	}
}

// marshalSnPeersRaw builds an sn_peers frame with arbitrary (possibly malformed)
// v4/v6 blobs, for exercising the decoder's tolerance.
func marshalSnPeersRaw(v4blob, v6blob []byte) ([]byte, error) {
	return bencode.Marshal(SnPeers{MsgType: MsgTypeSnPeers, V4: v4blob, V6: v6blob})
}
