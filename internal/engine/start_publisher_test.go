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
)

// TestStartPublisherManifestLoadError covers
// startPublisher's `mf, err := LoadOrCreateManifest(...); if err != nil { return }`
// arm. Plant a directory at the PublisherManifest path so
// LoadOrCreateManifest fails on the read of an unreadable
// path. Engine.New surfaces a non-fatal warn ('publisher_start_err')
// rather than aborting startup.
func TestStartPublisherManifestLoadError(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	identPath := filepath.Join(dataDir, "identity.key")
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(identPath, priv, 0o600); err != nil {
		t.Fatal(err)
	}

	// Plant a directory at the manifest path so LoadOrCreateManifest's
	// ReadFile fails with a non-NotExist error.
	manifestDir := filepath.Join(dataDir, "manifest.json")
	if err := os.Mkdir(manifestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestDir, "blocker"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.ListenPort = 0
	cfg.ListenHost = "127.0.0.1"
	cfg.DisableDHT = false
	cfg.DHTInsecure = true
	cfg.DHTBootstrapAddrs = []string{"127.0.0.1:1"}
	cfg.DisableIPv6 = true
	cfg.NoUpload = true
	cfg.Seed = false
	cfg.IdentityPath = identPath
	cfg.ReputationPath = ""
	cfg.SeedListPath = ""
	cfg.BloomPath = ""
	cfg.TrustPath = ""
	cfg.PublisherManifest = manifestDir
	cfg.CompanionDir = ""
	cfg.CompanionFollowFile = ""

	eng, err := engine.New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("engine.New: %v (publisher manifest failure must be non-fatal)", err)
	}
	defer eng.Close()

	if eng.Publisher() != nil {
		t.Error("Publisher must be nil when manifest load failed")
	}
}

// TestStartPublisherDisablePublishMode exercises the
// `if e.cfg.DisableDHTPublish { ... } else { ... }` arm in
// startPublisher. With DisableDHTPublish=true, the keyword
// publisher worker is skipped but lookup + pointer
// putter/getter are still wired so the node can subscribe to
// other publishers.
func TestStartPublisherDisablePublishMode(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	identPath := filepath.Join(dataDir, "identity.key")
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(identPath, priv, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.ListenPort = 0
	cfg.ListenHost = "127.0.0.1"
	cfg.DisableDHT = false
	cfg.DisableDHTPublish = true // leech-only DHT mode
	cfg.DHTInsecure = true
	cfg.DHTBootstrapAddrs = []string{"127.0.0.1:1"}
	cfg.DisableIPv6 = true
	cfg.NoUpload = true
	cfg.Seed = false
	cfg.IdentityPath = identPath
	cfg.ReputationPath = ""
	cfg.SeedListPath = ""
	cfg.BloomPath = ""
	cfg.TrustPath = ""
	cfg.PublisherManifest = ""
	cfg.CompanionDir = ""
	cfg.CompanionFollowFile = ""

	eng, err := engine.New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	defer eng.Close()

	// Publisher must be nil in leech-only mode; lookup and
	// pointer pair must still be wired so subscribers can pull.
	if eng.Publisher() != nil {
		t.Error("Publisher must be nil with DisableDHTPublish=true")
	}
	if eng.Lookup() == nil {
		t.Error("Lookup must still be wired in leech-only DHT mode")
	}
	if eng.PointerPutter() == nil {
		t.Error("PointerPutter must still be wired (BEP-46 publishing is independent)")
	}
}

// TestStartPublisherFullPath exercises engine.New's startPublisher
// happy path: DHT enabled + identity loaded → publisher worker
// constructed, lookup wired, pointer putter/getter populated.
//
// Coverage gain comes from the previously-cold DHT-enabled body
// in engine.go (the publisher Submit / refresh ticker plumbing).
func TestStartPublisherFullPath(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	identPath := filepath.Join(dataDir, "identity.key")
	// Pre-populate identity so startPublisher doesn't fail on
	// generation. Persist a real ed25519 key with 0o600 perms.
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// Identity file must be 0o600 to satisfy LoadOrCreate's
	// permissions check.
	if err := os.MkdirAll(filepath.Dir(identPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(identPath, priv, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.ListenPort = 0
	cfg.ListenHost = "127.0.0.1"
	cfg.DisableDHT = false
	cfg.DHTInsecure = true
	cfg.DHTBootstrapAddrs = []string{"127.0.0.1:1"} // dead-end
	cfg.DisableIPv6 = true
	cfg.NoUpload = true
	cfg.Seed = false
	cfg.IdentityPath = identPath
	// Wire bloom + reputation + tracker so startPublisher's
	// `if e.tracker != nil { lookup.SetTracker }` and
	// `if e.bloom != nil { lookup.SetBloom }` arms fire too.
	cfg.ReputationPath = filepath.Join(dataDir, "rep.json")
	cfg.SeedListPath = ""
	cfg.BloomPath = filepath.Join(dataDir, "known-good.bloom")
	cfg.TrustPath = ""
	cfg.PublisherManifest = filepath.Join(dataDir, "manifest.json")
	cfg.CompanionDir = ""
	cfg.CompanionFollowFile = ""
	cfg.Regtest = true         // exercises the Regtest publisher options arm
	cfg.MinIndexerScore = 0.25 // exercises lookup.SetMinIndexerScore arm

	eng, err := engine.New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	defer eng.Close()

	if eng.Identity() == nil {
		t.Error("Identity should be populated when IdentityPath is set")
	}
	if eng.Publisher() == nil {
		t.Error("Publisher should be wired when DHT is enabled and identity loaded")
	}
	if eng.Lookup() == nil {
		t.Error("Lookup should be wired alongside Publisher")
	}
	if eng.PointerPutter() == nil {
		t.Error("PointerPutter should be wired alongside Publisher")
	}
	if eng.PointerGetter() == nil {
		t.Error("PointerGetter should be wired alongside Publisher")
	}

	// Adding a torrent here drives autoIndex's `if pub != nil`
	// arm: with the publisher running, autoIndex Submits a
	// PublishTask after GotInfo fires.
	srcPath := filepath.Join(dataDir, "src.bin")
	if err := os.WriteFile(srcPath, []byte(fillTo(32*1024)), 0o644); err != nil {
		t.Fatal(err)
	}
	mi, err := eng.CreateTorrent(engine.CreateTorrentOptions{Root: srcPath})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.AddTorrentMetaInfo(mi); err != nil {
		t.Fatal(err)
	}
	// Wait briefly for autoIndex to run and Submit to the publisher.
	time.Sleep(150 * time.Millisecond)
}
