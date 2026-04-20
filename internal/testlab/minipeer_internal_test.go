package testlab

import (
	"net"
	"testing"
)

// TestDialMiniPeerUnreachable covers the dial-failure error
// branch of DialMiniPeer. Targeting an unbound localhost port
// gets ECONNREFUSED quickly without timing out.
func TestDialMiniPeerUnreachable(t *testing.T) {
	t.Parallel()
	// Bind a listener, capture its addr, then close it so the
	// port is reliably unreachable.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()

	var ih [20]byte
	if _, err := DialMiniPeer(addr, ih); err == nil {
		t.Error("DialMiniPeer to a closed port should error")
	}
}

// TestSendRawWritesToConn covers the previously 0%-covered
// SendRaw helper by handing it a net.Pipe so we can verify
// the bytes hit the wire without spinning a real TCP listener.
func TestSendRawWritesToConn(t *testing.T) {
	t.Parallel()
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	mp := &MiniPeer{conn: a}
	payload := []byte("hello-world")

	// Read on a goroutine so SendRaw doesn't block on the pipe.
	doneRead := make(chan []byte, 1)
	go func() {
		buf := make([]byte, len(payload))
		_, _ = b.Read(buf)
		doneRead <- buf
	}()

	if err := mp.SendRaw(payload); err != nil {
		t.Fatalf("SendRaw: %v", err)
	}
	got := <-doneRead
	if string(got) != string(payload) {
		t.Errorf("read = %q, want %q", got, payload)
	}
}
