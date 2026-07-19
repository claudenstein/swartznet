package engine

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
)

func layerDEngine(t *testing.T, mutate func(*config.Config)) *Engine {
	t.Helper()
	cfg := config.Config{
		DataDir:           filepath.Join(t.TempDir(), "data"),
		ListenHost:        "127.0.0.1",
		ListenPort:        0,
		DisableIPv6:       true,
		DHTInsecure:       true,
		DHTBootstrapAddrs: []string{"127.0.0.1:1"}, // dead-end: never leak to the public DHT
		Seed:              false,
		NoUpload:          true,
		PublisherPath:     filepath.Join(t.TempDir(), "publisher.json"),
		LayerDMode:        "legacy",
	}
	if mutate != nil {
		mutate(&cfg)
	}
	e, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func setTestSigner(t *testing.T, e *Engine) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var arr [32]byte
	copy(arr[:], pub)
	e.SetSigner(priv, arr)
}

// TestLayerDLookupAliveWithDHT: with the DHT enabled the read side is built at
// New, before any identity — a node can search Layer D leech-only.
func TestLayerDLookupAliveWithDHT(t *testing.T) {
	t.Parallel()
	e := layerDEngine(t, nil)
	if e.DHTLookup() == nil {
		t.Fatal("DHTLookup nil with DHT enabled")
	}
	if e.DHTPublisher() != nil {
		t.Fatal("DHTPublisher built before an identity was set")
	}
}

// TestLayerDPublisherBuiltOnSigner: an identity brings up the write side, and
// the self-pubkey enters the lookup set so a node finds its own published hits.
func TestLayerDPublisherBuiltOnSigner(t *testing.T) {
	t.Parallel()
	e := layerDEngine(t, nil)
	setTestSigner(t, e)
	if e.DHTPublisher() == nil {
		t.Fatal("DHTPublisher not built after SetSigner")
	}
	if len(e.DHTLookup().Indexers()) != 1 {
		t.Errorf("self-pubkey not added to the lookup set: %d indexers", len(e.DHTLookup().Indexers()))
	}
}

// TestLayerDNoDHTPublishSuppressesWriteSide is the privacy cascade: with
// --no-dht-publish the write side is NOT built (no announcing under the user's
// key), but the read side stays alive (leech-only Layer D).
func TestLayerDNoDHTPublishSuppressesWriteSide(t *testing.T) {
	t.Parallel()
	e := layerDEngine(t, func(c *config.Config) { c.DisableDHTPublish = true })
	setTestSigner(t, e)
	if e.DHTPublisher() != nil {
		t.Fatal("publisher built despite --no-dht-publish")
	}
	if e.DHTLookup() == nil {
		t.Fatal("lookup must stay alive leech-only")
	}
}

// TestLayerDNoIndexSuppressesWriteSide: --no-index also suppresses publishing
// under the identity (the publisher bit cascade), read side stays alive.
func TestLayerDNoIndexSuppressesWriteSide(t *testing.T) {
	t.Parallel()
	e := layerDEngine(t, func(c *config.Config) { c.NoIndex = true })
	setTestSigner(t, e)
	if e.DHTPublisher() != nil {
		t.Fatal("publisher built despite --no-index")
	}
	if e.DHTLookup() == nil {
		t.Fatal("lookup must stay alive with --no-index")
	}
}

// TestLayerDDisabledLeavesBothNil: with the DHT off there is no Layer D at all.
func TestLayerDDisabledLeavesBothNil(t *testing.T) {
	t.Parallel()
	e := layerDEngine(t, func(c *config.Config) {
		c.DisableDHT = true
		c.DHTInsecure = false
		c.DHTBootstrapAddrs = nil
	})
	setTestSigner(t, e)
	if e.DHTLookup() != nil || e.DHTPublisher() != nil {
		t.Fatal("Layer D must be absent when the DHT is disabled")
	}
	// PublisherStatus is still safe to call (returns zero value).
	if st := e.PublisherStatus(); st.TotalKeywords != 0 {
		t.Errorf("status = %+v", st)
	}
}

// TestPublishTorrentNoopWithoutPublisher: publishing is a safe no-op when the
// write side was never built.
func TestPublishTorrentNoopWithoutPublisher(t *testing.T) {
	t.Parallel()
	e := layerDEngine(t, func(c *config.Config) { c.DisableDHTPublish = true })
	setTestSigner(t, e)
	// Reaches the nil-publisher guard without panicking.
	e.publishTorrent(&Handle{})
}
