// Live mainline DHT smoke test for the SwartzNet publisher path.
//
// Spins up an anacrolix/dht/v2 server, lets it bootstrap against
// the public router nodes for ~10 seconds, then runs an
// AnacrolixPutter Put cycle for a synthetic keyword and reports
// the result. Then runs an AnacrolixGetter Get against the same
// (pubkey, salt) target to confirm the round trip survives the
// real network.
//
// Run from the SwartzNet repo root with:
//   go run /tmp/dht_smoke/main.go
//
// Exits with status 0 if Put + Get both succeed.
package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/anacrolix/dht/v2"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(logger); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "PASS")
}

func run(log *slog.Logger) error {
	cfg := dht.NewDefaultServerConfig()
	srv, err := dht.NewServer(cfg)
	if err != nil {
		return fmt.Errorf("dht.NewServer: %w", err)
	}
	defer srv.Close()

	log.Info("dht.bootstrap_started")
	bootstrapCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := srv.Bootstrap(); err != nil {
		log.Warn("dht.bootstrap_warn", "err", err)
	}
	// Wait for the routing table to populate.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		stats := srv.Stats()
		log.Info("dht.bootstrap_progress",
			"good_nodes", stats.GoodNodes,
			"nodes", stats.Nodes,
			"outbound", stats.OutboundQueriesAttempted)
		if stats.GoodNodes >= 8 {
			break
		}
		select {
		case <-bootstrapCtx.Done():
		case <-time.After(2 * time.Second):
		}
	}
	stats := srv.Stats()
	log.Info("dht.bootstrap_done",
		"good_nodes", stats.GoodNodes,
		"nodes", stats.Nodes)
	if stats.GoodNodes < 1 {
		return fmt.Errorf("no good DHT nodes after bootstrap")
	}

	// Generate a fresh ephemeral identity for this smoke test.
	// We don't reuse the user's real publisher key — the goal is
	// to validate the wire path, not to leak their identity into
	// the live DHT.
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("ed25519 keygen: %w", err)
	}
	log.Info("identity.ephemeral", "pubkey_first_8", fmt.Sprintf("%x", pub[:8]))

	putter, err := dhtindex.NewAnacrolixPutter(srv, priv)
	if err != nil {
		return fmt.Errorf("NewAnacrolixPutter: %w", err)
	}
	getter, err := dhtindex.NewAnacrolixGetter(srv)
	if err != nil {
		return fmt.Errorf("NewAnacrolixGetter: %w", err)
	}

	keyword := fmt.Sprintf("swartznet_smoke_%d", time.Now().Unix())
	salt, err := dhtindex.SaltForKeyword(keyword)
	if err != nil {
		return fmt.Errorf("SaltForKeyword: %w", err)
	}

	value := dhtindex.KeywordValue{
		Hits: []dhtindex.KeywordHit{
			{
				IH: bytes.Repeat([]byte{0xab}, 20),
				N:  "smoke test",
				S:  1,
			},
		},
	}

	putCtx, putCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer putCancel()
	log.Info("put.start", "keyword", keyword)
	putStart := time.Now()
	if err := putter.Put(putCtx, salt, value); err != nil {
		return fmt.Errorf("Put: %w", err)
	}
	log.Info("put.ok", "elapsed", time.Since(putStart).String())

	// Now try to read it back. The same DHT server holds the put
	// locally, so this should succeed even if the put didn't
	// reach distant nodes — but it also tests the get path end
	// to end against any remote node that did accept the put.
	getCtx, getCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer getCancel()
	var pk32 [32]byte
	copy(pk32[:], pub)
	log.Info("get.start", "keyword", keyword)
	getStart := time.Now()
	got, err := getter.Get(getCtx, pk32, salt)
	if err != nil {
		return fmt.Errorf("Get: %w", err)
	}
	log.Info("get.ok",
		"elapsed", time.Since(getStart).String(),
		"hits", len(got.Hits))
	if len(got.Hits) != 1 {
		return fmt.Errorf("Get returned %d hits, want 1", len(got.Hits))
	}
	if got.Hits[0].N != "smoke test" {
		return fmt.Errorf("Get returned name %q, want 'smoke test'", got.Hits[0].N)
	}
	return nil
}
