// Package wirecompat holds the in-process multi-engine test harness — the
// standard vehicle for deterministic engine, wire, and (later) search-layer
// tests. Deterministic helpers and single-engine tests run in CI -race; the
// timing-sensitive multi-client transfer scenarios live in the scenarios
// subpackage, which CI excludes.
package wirecompat

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// Node is one in-process engine with its own data root.
type Node struct {
	Eng     *engine.Engine
	DataDir string
}

// Cluster is a set of in-process engines wired for deterministic tests: DHT
// off, OS-assigned ports, seeding enabled (NoUpload=false — anacrolix serves
// nothing otherwise).
type Cluster struct {
	Nodes []*Node
}

// NewCluster spawns n engines, each on a fresh temp root. Engines close in
// reverse order at test cleanup.
func NewCluster(t *testing.T, n int) *Cluster {
	t.Helper()
	c := &Cluster{}
	for i := 0; i < n; i++ {
		root := t.TempDir()
		cfg := config.Config{
			DataDir:               filepath.Join(root, "data"),
			ListenPort:            0,
			DisableDHT:            true,
			DisablePortForwarding: true,
			Seed:                  true,
		}
		eng, err := engine.New(t.Context(), cfg, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
		if err != nil {
			t.Fatalf("node %d: %v", i, err)
		}
		c.Nodes = append(c.Nodes, &Node{Eng: eng, DataDir: cfg.DataDir})
	}
	t.Cleanup(func() {
		for i := len(c.Nodes) - 1; i >= 0; i-- {
			_ = c.Nodes[i].Eng.Close()
		}
	})
	return c
}

// DeterministicPayload builds size bytes from a fixed LCG so fixtures are
// reproducible without Math.random.
func DeterministicPayload(size int) []byte {
	out := make([]byte, size)
	s := uint32(0xdeadbeef)
	for i := range out {
		s = s*1664525 + 1013904223
		out[i] = byte(s >> 16)
	}
	return out
}

// BuildFixture writes a deterministic payload file under dir and returns its
// metainfo (32 KiB pieces) plus the payload. No tracker, no create command —
// programmatic metainfo only.
func BuildFixture(t *testing.T, dir, name string, size int) (*metainfo.MetaInfo, []byte) {
	t.Helper()
	payload := DeterministicPayload(size)
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	info := metainfo.Info{PieceLength: 32 * 1024}
	if err := info.BuildFromFilePath(path); err != nil {
		t.Fatal(err)
	}
	mi := &metainfo.MetaInfo{InfoBytes: bencode.MustMarshal(info)}
	return mi, payload
}
