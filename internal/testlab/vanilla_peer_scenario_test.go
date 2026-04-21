package testlab_test

import (
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/testlab"
)

// TestScenarioVanillaPeerReceivesNoSNSearchFrames closes the
// 8.1-A cell of the wire-compat matrix: a peer that does NOT
// advertise sn_search in its LTEP handshake must NEVER receive
// an sn_search extension frame from the engine.
//
// The peer the engine is talking to looks indistinguishable from
// a mainline libtorrent/qBittorrent — an empty `m` dict in the
// LTEP handshake. After the handshake the engine may send any
// standard peer-wire traffic (bitfield, keepalive, unchoke, ...)
// but MUST NOT pick an arbitrary extension id and push an
// sn_search query/announce frame at us.
//
// The test also inspects the engine's own LTEP handshake reply:
// that reply can freely advertise sn_search in its `m` dict (the
// engine doesn't know yet what we support), but no subsequent
// extension message may carry an ID that matches the engine's
// sn_search slot — because using that ID implies "this message
// is sn_search" and a vanilla peer would not know how to decode
// it.
func TestScenarioVanillaPeerReceivesNoSNSearchFrames(t *testing.T) {
	c := testlab.NewCluster(t, 1)
	// Seed the node's index so the engine has something it might
	// try to gossip over sn_search (PeerAnnounce etc.) — the test
	// still expects zero sn_search frames because the remote peer
	// did not advertise the extension.
	c.Nodes[0].IndexTorrent(t, 0x11, "wire-compat-vanilla-scenario")

	addr := fmt.Sprintf("127.0.0.1:%d", c.Nodes[0].Eng.LocalPort())
	mp, err := testlab.DialVanillaMiniPeer(addr, c.SharedInfoHash())
	if err != nil {
		c.DumpLogs(t)
		t.Fatalf("DialVanillaMiniPeer: %v", err)
	}
	defer mp.Close()

	// Snapshot the extension id the engine picked for sn_search.
	// If it's 0 the engine didn't advertise at all — that's fine,
	// the rest of the loop will simply never see a matching msg.
	engineSNID := mp.RemoteSnSearchID()
	t.Logf("engine advertised sn_search with ext_id=%d (vanilla peer ignores)", engineSNID)

	// Drain incoming messages for a window long enough for any
	// post-handshake gossip/PeerAnnounce to arrive. Anything with
	// ext_id == engineSNID (and engineSNID != 0) is a wire-compat
	// violation.
	deadline := time.Now().Add(2 * time.Second)
	var snSearchFrames int
	for time.Now().Before(deadline) {
		msg, err := mp.RecvMessage(200 * time.Millisecond)
		if err != nil {
			// A read timeout is expected — that's how we know the
			// engine is NOT speaking to us anymore. Any other
			// error (EOF, reset) is also acceptable as long as it
			// happened after the handshake.
			if isTimeout(err) || err == io.EOF {
				continue
			}
			// Soft-continue on other errors; the loop itself will
			// exit on deadline. Logging is enough — the assertion
			// is on snSearchFrames, not on the stream shape.
			t.Logf("recv err (non-fatal): %v", err)
			break
		}
		if len(msg) < 2 {
			continue
		}
		if msg[0] != 20 {
			// Not a BEP-10 extended message. Could be a bitfield,
			// have, keepalive — all fine.
			continue
		}
		extID := int(msg[1])
		if extID == 0 {
			// ext_id 0 is the LTEP handshake itself. The engine
			// is allowed to send us a handshake advertising
			// sn_search; we simply ignore it. Not a violation.
			continue
		}
		if engineSNID != 0 && extID == engineSNID {
			snSearchFrames++
			t.Errorf("engine sent sn_search frame (ext_id=%d) to a peer that did not advertise sn_search; payload_len=%d",
				extID, len(msg)-2)
		}
	}

	if snSearchFrames != 0 {
		c.DumpLogs(t)
	}
}

// isTimeout reports whether err is a net.Error flagged as a timeout.
// The MiniPeer sets SetReadDeadline so a normal "nobody is talking"
// return comes back as a timeout error, which we treat as "no
// more messages this round" rather than a test failure.
func isTimeout(err error) bool {
	type timeoutErr interface{ Timeout() bool }
	te, ok := err.(timeoutErr)
	return ok && te.Timeout()
}
