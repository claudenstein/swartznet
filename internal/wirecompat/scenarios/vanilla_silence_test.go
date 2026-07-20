package scenarios

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/wirecompat"
)

// TestVanillaPeerSeesNoSnSearch is the load-bearing mainline-compat gate: a
// peer whose LTEP `m` dict omits sn_search receives ZERO sn_search frames from
// the engine, even while a capable peer on the same engine DOES get gossiped a
// peer_announce (proving the engine would have talked if the peer had asked).
//
// Timing-sensitive (real loopback bytes + drain windows), so it lives in the
// CI-excluded scenarios package and runs under the local `go test -race`.
func TestVanillaPeerSeesNoSnSearch(t *testing.T) {
	if testing.Short() {
		t.Skip("raw-socket loopback silence test; run without -short")
	}
	c := wirecompat.NewCluster(t, 1)
	eng := c.Nodes[0].Eng

	// Seed a fixture so the engine accepts inbound connections on its infohash.
	contentDir := filepath.Join(c.Nodes[0].DataDir, "content")
	mi, _ := wirecompat.BuildFixture(t, contentDir, "fixture.bin", 64*1024)
	if _, err := eng.AddTorrentMetaInfoSeedFrom(mi, filepath.Join(contentDir, "fixture.bin")); err != nil {
		t.Fatal(err)
	}
	ih := [20]byte(mi.HashInfoBytes())
	addr := fmt.Sprintf("127.0.0.1:%d", eng.LocalPort())

	// Control: a CAPABLE peer must receive the engine's peer_announce, proving
	// the engine actively gossips sn_search to peers that advertise it.
	capable, err := wirecompat.DialMiniPeer(addr, ih)
	if err != nil {
		t.Fatalf("dial capable peer: %v", err)
	}
	defer capable.Close()
	if capable.RemoteSnSearchID() == 0 {
		t.Fatal("engine did not advertise sn_search in its LTEP m dict")
	}
	if !capable.SawSnSearchWithin(3 * time.Second) {
		t.Fatal("capable peer never received the engine's peer_announce — the silence test would be vacuous")
	}

	// The subject: a VANILLA peer (empty m dict) must receive nothing.
	vanilla, err := wirecompat.DialVanillaMiniPeer(addr, ih)
	if err != nil {
		t.Fatalf("dial vanilla peer: %v", err)
	}
	defer vanilla.Close()
	if vanilla.SawSnSearchWithin(2 * time.Second) {
		t.Fatal("vanilla peer received an sn_search frame — mainline-compat VIOLATED")
	}
}
