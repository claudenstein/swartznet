package engine_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
	"github.com/swartznet/swartznet/internal/trust"
)

// TestTorrentSnapshotsMarksTrustedPublisher covers the
// previously-uncovered TrustedPublisher fill-in branch in
// TorrentSnapshots: a torrent signed by a publisher in the
// engine's trust store appears with TrustedPublisher=true.
func TestTorrentSnapshotsMarksTrustedPublisher(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	trustPath := filepath.Join(dataDir, "trust.json")

	// Pre-populate the trust store with the publisher key we're
	// about to sign with.
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubHex := ""
	const hex = "0123456789abcdef"
	for _, b := range pub {
		pubHex += string([]byte{hex[b>>4], hex[b&0x0f]})
	}
	ts, err := trust.LoadOrCreate(trustPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := ts.Add(pubHex, "test-publisher"); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true
	cfg.TrustPath = trustPath
	// Clear the other XDG file-backed paths for hermetic test.
	cfg.IdentityPath = ""
	cfg.ReputationPath = ""
	cfg.SeedListPath = ""
	cfg.BloomPath = ""

	eng, err := engine.New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	defer eng.Close()

	// Create a signed torrent and add it.
	srcPath := filepath.Join(dataDir, "src.bin")
	if err := os.WriteFile(srcPath, []byte(fillTo(32*1024)), 0o644); err != nil {
		t.Fatal(err)
	}
	torrentPath := filepath.Join(dataDir, "x.torrent")
	if _, _, err := eng.CreateTorrentFile(engine.CreateTorrentOptions{
		Root:     srcPath,
		SignWith: priv,
	}, torrentPath); err != nil {
		t.Fatalf("CreateTorrentFile: %v", err)
	}
	if _, err := eng.AddTorrentFile(torrentPath); err != nil {
		t.Fatalf("AddTorrentFile: %v", err)
	}

	snaps := eng.TorrentSnapshots()
	if len(snaps) != 1 {
		t.Fatalf("len(snaps) = %d, want 1", len(snaps))
	}
	if !snaps[0].TrustedPublisher {
		t.Errorf("TrustedPublisher = false, want true (publisher %s is in trust store)", pubHex)
	}
}
