package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
)

func rvTestServer(t *testing.T, opts Options) *Server {
	t.Helper()
	s := NewWithOptions("localhost:0", slog.New(slog.NewTextHandler(io.Discard, nil)), opts)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	return s
}

func getStatus(t *testing.T, addr string) StatusResponse {
	t.Helper()
	resp, err := http.Get("http://" + addr + "/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var st StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	return st
}

// TestStatusRendezvousBlock: the rendezvous block reflects the collaborator when
// wired, and is omitted entirely when it is not (rendezvous off).
func TestStatusRendezvousBlock(t *testing.T) {
	// Wired: 3 swarms, gossiping.
	s := rvTestServer(t, Options{RendezvousStatus: func() (int, bool) { return 3, true }})
	st := getStatus(t, s.Addr())
	if st.Rendezvous == nil {
		t.Fatal("rendezvous block missing when collaborator set")
	}
	if st.Rendezvous.Swarms != 3 || !st.Rendezvous.PeerGossip {
		t.Fatalf("rendezvous = %+v, want {Swarms:3 PeerGossip:true}", *st.Rendezvous)
	}

	// Not wired: block omitted.
	s2 := rvTestServer(t, Options{})
	if st2 := getStatus(t, s2.Addr()); st2.Rendezvous != nil {
		t.Fatalf("rendezvous block present when collaborator nil: %+v", *st2.Rendezvous)
	}
}
