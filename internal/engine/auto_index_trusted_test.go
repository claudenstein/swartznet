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
	"time"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
	"github.com/swartznet/swartznet/internal/trust"
)

// TestAutoIndexAddsTrustedPublisherInfohashToBloom covers the
// previously-uncovered branch in autoIndex where a torrent
// signed by a trusted publisher is auto-confirmed into the
// known-good Bloom filter.
func TestAutoIndexAddsTrustedPublisherInfohashToBloom(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	trustPath := filepath.Join(dataDir, "trust.json")
	bloomPath := filepath.Join(dataDir, "bloom.bin")

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
	if err := ts.Add(pubHex, "trusted-pub"); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true
	cfg.TrustPath = trustPath
	cfg.BloomPath = bloomPath
	cfg.IdentityPath = ""
	cfg.ReputationPath = ""
	cfg.SeedListPath = ""

	eng, err := engine.New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	defer eng.Close()

	srcPath := filepath.Join(dataDir, "src.bin")
	if err := os.WriteFile(srcPath, []byte(fillTo(32*1024)), 0o644); err != nil {
		t.Fatal(err)
	}
	torrentPath := filepath.Join(dataDir, "x.torrent")
	ihHex, _, err := eng.CreateTorrentFile(engine.CreateTorrentOptions{
		Root:     srcPath,
		SignWith: priv,
	}, torrentPath)
	if err != nil {
		t.Fatalf("CreateTorrentFile: %v", err)
	}
	if _, err := eng.AddTorrentFile(torrentPath); err != nil {
		t.Fatalf("AddTorrentFile: %v", err)
	}

	// autoIndex runs in a goroutine; poll the bloom for the
	// torrent's infohash.
	ihBytes := make([]byte, 20)
	for i := 0; i < 20; i++ {
		hi := ihHex[i*2]
		lo := ihHex[i*2+1]
		ihBytes[i] = hexNibble(hi)<<4 | hexNibble(lo)
	}
	bf := eng.KnownGoodBloom()
	if bf == nil {
		t.Fatal("KnownGoodBloom should be populated when BloomPath is set")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if bf.Test(ihBytes) {
			return // success
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("Bloom never received the trusted-publisher infohash %s", ihHex)
}

// hexNibble decodes a single ASCII hex char.
func hexNibble(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return 10 + c - 'a'
	case c >= 'A' && c <= 'F':
		return 10 + c - 'A'
	}
	return 0
}
