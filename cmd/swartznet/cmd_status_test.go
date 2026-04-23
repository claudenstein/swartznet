package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/httpapi"
)

// TestEmitStatusTextIncludesDHTWhenPresent covers the new
// "DHT routing table" block in emitStatusText's output. Two
// sub-cases to lock in the load-bearing contract: the block
// must appear when the daemon reported a "dht" field, and
// must be omitted entirely when it didn't (so operators
// running a DHT-off daemon don't see a misleading "0 / 0"
// line).
func TestEmitStatusTextIncludesDHTWhenPresent(t *testing.T) {
	t.Run("populated", func(t *testing.T) {
		resp := &httpapi.StatusResponse{
			Local: httpapi.LocalStatus{Indexed: true, DocCount: 5},
			DHT:   &httpapi.DHTStatus{GoodNodes: 17, Nodes: 42},
		}
		var buf bytes.Buffer
		if code := emitStatusText(&buf, resp); code != exitOK {
			t.Fatalf("emitStatusText returned %d, want exitOK", code)
		}
		out := buf.String()
		if !strings.Contains(out, "DHT routing table:") {
			t.Errorf("missing header; out=%q", out)
		}
		if !strings.Contains(out, "good nodes:     17") {
			t.Errorf("missing good_nodes row; out=%q", out)
		}
		if !strings.Contains(out, "total nodes:    42") {
			t.Errorf("missing total_nodes row; out=%q", out)
		}
	})

	t.Run("omitted_when_nil", func(t *testing.T) {
		resp := &httpapi.StatusResponse{
			Local: httpapi.LocalStatus{Indexed: true, DocCount: 5},
			// DHT intentionally nil — simulating a DHT-disabled daemon.
		}
		var buf bytes.Buffer
		if code := emitStatusText(&buf, resp); code != exitOK {
			t.Fatalf("emitStatusText returned %d, want exitOK", code)
		}
		out := buf.String()
		if strings.Contains(out, "DHT routing table:") {
			t.Errorf("DHT block should be omitted when Response.DHT is nil; out=%q", out)
		}
	})
}
