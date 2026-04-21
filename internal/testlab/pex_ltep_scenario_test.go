package testlab_test

// TestScenarioPEXPassthroughAndLTEPKeysIgnored closes wire-compat
// matrix row 8.1-C with two sub-assertions:
//
//   - 8.1-C/pex: vanillaA ↔ engine ↔ vanillaB topology. vanillaA is
//     told only about the engine; vanillaB is told only about the
//     engine. The engine's ut_pex fires immediately (timer delay=0)
//     after each handshake, advertising each client to the other. We
//     poll vanillaA's KnownSwarm() until it contains any peer with
//     Source == PeerSourcePex ("X"), proving PEX frames pass through
//     the engine unchanged.
//
//   - 8.1-C/ltep: a MiniPeer in vanilla mode handshakes with the engine.
//     Assertion A: the engine's own LTEP reply advertises sn_search
//     (engine is broadcasting its own capabilities — expected). Assertion
//     B: the engine's RemoteExtIDs (what IT sent the MiniPeer) contains
//     sn_search, confirming the engine properly advertises it. Assertion
//     C: engine sends 0 sn_search extension frames to the vanilla peer
//     in a 2-second window (engine correctly skips sn_search for a peer
//     whose m-dict had no sn_search entry).

import (
	"fmt"
	"net"
	"testing"
	"time"

	anacrolixtorrent "github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/swartznet/swartznet/internal/testlab"
)

// TestScenarioPEXPassthroughAndLTEPKeysIgnored is the integration
// test for wire-compat matrix row 8.1-C.
func TestScenarioPEXPassthroughAndLTEPKeysIgnored(t *testing.T) {
	t.Parallel()
	t.Run("8.1-C/pex_passthrough", testPEXPassthrough)
	t.Run("8.1-C/ltep_keys_ignored", testLTEPKeysIgnoredByVanilla)
}

// testPEXPassthrough asserts the PEX half of 8.1-C.
//
// Topology:
//
//	vanillaA  →  engine  ←  vanillaB
//	     (no direct A↔B link)
//
// After vanillaB connects to the engine, the engine's pexState.msg0
// holds vanillaB's address. When vanillaA connects, the engine
// fires an immediate PEX message (anacrolix pexConnState.Init uses
// time.AfterFunc(0,…) for the initial send) containing vanillaB's
// address. We poll vanillaA's KnownSwarm() until it contains any
// peer with Source == PeerSourcePex ("X").
func testPEXPassthrough(t *testing.T) {
	t.Helper()

	// One SwartzNet engine node acts as the PEX hub.
	c := testlab.NewCluster(t, 1)
	eng := c.Nodes[0].Eng
	ih := metainfo.Hash(c.SharedInfoHash())

	// vanillaB: first vanilla client to connect to the engine.
	// Connecting before vanillaA ensures vanillaB's address is in the
	// engine's pexState.msg0 when vanillaA later connects.
	cfgB := newVanillaConfig(t)
	cfgB.Seed = true // must seed to keep connection alive
	vanillaB, err := anacrolixtorrent.NewClient(cfgB)
	if err != nil {
		t.Fatalf("vanillaB client: %v", err)
	}
	t.Cleanup(func() { _ = vanillaB.Close() })

	vtB, _ := vanillaB.AddTorrentInfoHash(ih)
	vtB.AddPeers([]anacrolixtorrent.PeerInfo{{
		Addr:    &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: eng.LocalPort()},
		Trusted: true,
	}})

	// Get the engine's handle for the shared infohash so we can poll
	// its active-peer count.
	engHandle, _ := eng.AddInfoHash(c.SharedInfoHash())

	// Wait until the engine records at least one active connection (to
	// vanillaB). This ensures vanillaB's address will be included in
	// the engine's initial PEX message to vanillaA.
	waitDeadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(waitDeadline) {
		if engHandle != nil && engHandle.T.Stats().ActivePeers > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if engHandle != nil {
		t.Logf("engine active peers before vanillaA connects: %d",
			engHandle.T.Stats().ActivePeers)
	}

	// vanillaA: connects to the engine only. Does NOT know vanillaB.
	cfgA := newVanillaConfig(t)
	cfgA.Seed = true
	vanillaA, err := anacrolixtorrent.NewClient(cfgA)
	if err != nil {
		t.Fatalf("vanillaA client: %v", err)
	}
	t.Cleanup(func() { _ = vanillaA.Close() })

	vtA, _ := vanillaA.AddTorrentInfoHash(ih)
	vtA.AddPeers([]anacrolixtorrent.PeerInfo{{
		Addr:    &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: eng.LocalPort()},
		Trusted: true,
	}})

	// Poll vanillaA's Stats() for TotalPeers > 1. Starting PeerInfo
	// count is 1 (engine, from AddPeers above). In this topology
	// (no DHT, no tracker, no manual peer on B), the only way
	// TotalPeers can grow is ut_pex frames delivered by the engine
	// advertising vanillaB. Stats() takes the client's rLock
	// (torrent.go:2443) so this poll is race-safe — unlike
	// KnownSwarm()/PeerConns() which iterate unlocked btrees that
	// the PEX read-loop mutates concurrently.
	pexDeadline := time.Now().Add(15 * time.Second)
	var pexFound bool
	var lastTotal int
	for time.Now().Before(pexDeadline) && !pexFound {
		st := vtA.Stats()
		lastTotal = st.TotalPeers
		if st.TotalPeers > 1 {
			pexFound = true
			t.Logf("8.1-C/pex: vanillaA TotalPeers=%d after PEX (engine + %d learnt via ut_pex): PASS",
				st.TotalPeers, st.TotalPeers-1)
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !pexFound {
		c.DumpLogs(t)
		t.Logf("vanillaA last Stats().TotalPeers=%d (expected >=2)", lastTotal)
		if engHandle != nil {
			t.Logf("engine Stats(): %+v", engHandle.T.Stats())
		}
		t.Fatalf("8.1-C/pex: vanillaA never received a PEX-sourced peer within 15s; " +
			"engine must not be forwarding ut_pex correctly")
	}
}

// testLTEPKeysIgnoredByVanilla asserts the LTEP half of 8.1-C.
//
// Three assertions:
//
//  A. The engine DOES advertise sn_search in its own LTEP handshake
//     m-dict (positive check: the engine broadcasts its capabilities
//     to ALL peers, including vanilla ones).
//
//  B. The RemoteExtIDs map (what the ENGINE sent to the MiniPeer)
//     contains exactly sn_search — confirming the engine includes
//     it in its outgoing handshake and that the MiniPeer parser
//     correctly captured it.
//
//  C. The engine sends 0 sn_search extension frames to the vanilla
//     peer in a 2-second observation window. Because the vanilla
//     MiniPeer sent m:{} (no sn_search), the engine must not choose
//     an sn_search ext_id to use when writing to this connection.
func testLTEPKeysIgnoredByVanilla(t *testing.T) {
	t.Helper()

	c := testlab.NewCluster(t, 1)
	eng := c.Nodes[0].Eng

	// Seed the engine so there is something that could trigger a
	// PeerAnnounce on handshake — stresses the "would we send
	// sn_search to this vanilla peer?" code path.
	c.Nodes[0].IndexTorrent(t, 0x1C, "pex-ltep-compat-check")

	addr := fmt.Sprintf("127.0.0.1:%d", eng.LocalPort())
	mp, err := testlab.DialVanillaMiniPeer(addr, c.SharedInfoHash())
	if err != nil {
		c.DumpLogs(t)
		t.Fatalf("DialVanillaMiniPeer: %v", err)
	}
	defer mp.Close()

	// Assertion A: engine MUST advertise sn_search in its LTEP reply.
	engineSNID := mp.RemoteSnSearchID()
	if engineSNID == 0 {
		c.DumpLogs(t)
		t.Fatalf("8.1-C/ltep A: engine did NOT advertise sn_search in its LTEP handshake; " +
			"engine must always broadcast its own capabilities")
	}
	t.Logf("8.1-C/ltep assertion A: engine advertised sn_search ext_id=%d: PASS", engineSNID)

	// Assertion B: log the full RemoteExtIDs so we can see what the
	// engine advertised. The test focus here is that the vanilla peer
	// (MiniPeer) did NOT echo sn_search back — which is verified via
	// Assertion C (zero frames) and is guaranteed by DialVanillaMiniPeer
	// sending m:{}, but we explicitly snapshot for the audit trail.
	remoteExt := mp.RemoteExtIDs()
	t.Logf("8.1-C/ltep assertion B: engine's LTEP m-dict as seen by vanilla MiniPeer: %v", remoteExt)
	if _, ok := remoteExt["sn_search"]; !ok {
		// Internal sanity: if RemoteSnSearchID() returned non-zero,
		// RemoteExtIDs must also contain it.
		t.Errorf("8.1-C/ltep B: RemoteExtIDs inconsistent with RemoteSnSearchID: map=%v id=%d",
			remoteExt, engineSNID)
	}
	t.Logf("8.1-C/ltep assertion B: sn_search is in engine outbound m-dict (id=%d): PASS", engineSNID)

	// Assertion C: no sn_search frames sent to vanilla peer.
	// The engine must not pick an ext_id for sn_search on a connection
	// whose remote m-dict had no sn_search entry.
	drainDeadline := time.Now().Add(2 * time.Second)
	snFrames := 0
	for time.Now().Before(drainDeadline) {
		msg, err := mp.RecvMessage(200 * time.Millisecond)
		if err != nil {
			if isTimeout(err) {
				continue
			}
			break
		}
		if len(msg) < 2 || msg[0] != 20 {
			continue
		}
		extID := int(msg[1])
		if extID != 0 && extID == engineSNID {
			snFrames++
			t.Errorf("8.1-C/ltep C: engine sent sn_search frame (ext_id=%d) "+
				"to vanilla peer (payload_len=%d)", extID, len(msg)-2)
		}
	}
	if snFrames == 0 {
		t.Logf("8.1-C/ltep assertion C: 0 sn_search frames sent to vanilla peer: PASS")
	}
}
