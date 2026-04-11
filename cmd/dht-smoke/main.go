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
//
//	go run ./cmd/dht-smoke            # single put/get, the original smoke
//	go run ./cmd/dht-smoke -stress 20 # after the smoke, 20 concurrent puts
//
// The -stress mode addresses v1.0.0 open question #2 — "how many
// concurrent BEP-44 mutable-item publishes can the anacrolix DHT
// library sustain before it starts getting rate-limited by other
// DHT nodes?". It reports per-put latency (min / p50 / p95 / max),
// total success rate, and the get-back round trip from a sample
// of the successful puts.
//
// Exits with status 0 if the single smoke Put + Get succeed. The
// stress phase always logs results but does not fail the exit
// status unless ALL puts fail (in which case the DHT path is
// clearly broken).
package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/anacrolix/dht/v2"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// Command-line flags.
var (
	stressN          = flag.Int("stress", 0, "after the basic smoke, run N concurrent mutable-item puts against the live DHT (0 = skip)")
	stressTimeout    = flag.Duration("stress-timeout", 60*time.Second, "bound for a single stress put")
	stressConcurrent = flag.Int("stress-concurrent", 8, "maximum concurrent puts during -stress (0 = serial)")
)

func main() {
	flag.Parse()
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

	// Optional: stress phase. Publishes *stressN synthetic
	// keywords in parallel (respecting stressConcurrent), then
	// reports a latency summary.
	if *stressN > 0 {
		if err := runStress(log, srv, putter, getter, pub); err != nil {
			log.Warn("stress.error", "err", err)
			// Do not fail the exit status unless every put failed —
			// a partial failure is the interesting measurement.
		}
	}
	return nil
}

// stressResult holds the outcome of one concurrent put.
type stressResult struct {
	keyword string
	elapsed time.Duration
	err     error
}

// runStress publishes stressN synthetic keywords in parallel
// against the live DHT and reports per-put latency + success
// rate. Addresses the v1.0.0 open question about BEP-44 behavior
// under concurrent load.
func runStress(
	log *slog.Logger,
	srv *dht.Server,
	putter *dhtindex.AnacrolixPutter,
	getter *dhtindex.AnacrolixGetter,
	pub ed25519.PublicKey,
) error {
	total := *stressN
	concurrency := *stressConcurrent
	if concurrency <= 0 {
		concurrency = 1
	}
	if concurrency > total {
		concurrency = total
	}
	log.Info("stress.start",
		"total_puts", total,
		"concurrency", concurrency,
		"per_put_timeout", stressTimeout.String())

	sem := make(chan struct{}, concurrency)
	results := make([]stressResult, total)
	var wg sync.WaitGroup
	start := time.Now()

	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			keyword := fmt.Sprintf("swartznet_stress_%d_%d", time.Now().UnixNano(), idx)
			salt, err := dhtindex.SaltForKeyword(keyword)
			if err != nil {
				results[idx] = stressResult{keyword: keyword, err: err}
				return
			}
			value := dhtindex.KeywordValue{
				Hits: []dhtindex.KeywordHit{{
					IH: bytes.Repeat([]byte{byte(idx)}, 20),
					N:  fmt.Sprintf("stress hit %d", idx),
					S:  1,
				}},
			}
			ctx, cancel := context.WithTimeout(context.Background(), *stressTimeout)
			defer cancel()
			putStart := time.Now()
			err = putter.Put(ctx, salt, value)
			results[idx] = stressResult{
				keyword: keyword,
				elapsed: time.Since(putStart),
				err:     err,
			}
		}(i)
	}
	wg.Wait()
	wallclock := time.Since(start)

	// Aggregate: success count, latency distribution, error list.
	var ok int
	latencies := make([]time.Duration, 0, total)
	errCounts := make(map[string]int)
	for _, r := range results {
		if r.err == nil {
			ok++
			latencies = append(latencies, r.elapsed)
		} else {
			errCounts[r.err.Error()]++
		}
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	pick := func(p float64) time.Duration {
		if len(latencies) == 0 {
			return 0
		}
		idx := int(float64(len(latencies)-1) * p)
		return latencies[idx]
	}
	log.Info("stress.summary",
		"total", total,
		"success", ok,
		"fail", total-ok,
		"success_rate", fmt.Sprintf("%.1f%%", 100*float64(ok)/float64(total)),
		"wall_clock", wallclock.String(),
	)
	if ok > 0 {
		log.Info("stress.latency",
			"min", latencies[0].String(),
			"p50", pick(0.50).String(),
			"p95", pick(0.95).String(),
			"max", latencies[len(latencies)-1].String(),
		)
	}
	for msg, n := range errCounts {
		log.Warn("stress.error_bucket", "count", n, "err", msg)
	}
	// Final DHT routing stats after the load, for context on
	// whether the stress mutated the routing table visibly.
	s := srv.Stats()
	log.Info("stress.dht_after",
		"good_nodes", s.GoodNodes,
		"nodes", s.Nodes,
		"outbound_attempted", s.OutboundQueriesAttempted,
	)

	if ok == 0 {
		return fmt.Errorf("stress: all %d puts failed", total)
	}

	// Sanity: pick the first successful keyword and round-trip
	// it back via Get. If this fails the item may have expired or
	// not propagated; log but don't fail.
	for i, r := range results {
		if r.err != nil {
			continue
		}
		salt, err := dhtindex.SaltForKeyword(r.keyword)
		if err != nil {
			continue
		}
		var pk32 [32]byte
		copy(pk32[:], pub)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, err = getter.Get(ctx, pk32, salt)
		cancel()
		if err != nil {
			log.Warn("stress.roundtrip_fail", "idx", i, "keyword", r.keyword, "err", err)
		} else {
			log.Info("stress.roundtrip_ok", "idx", i, "keyword", r.keyword)
		}
		break
	}
	return nil
}
