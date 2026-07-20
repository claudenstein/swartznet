package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/anacrolix/dht/v2"

	"github.com/swartznet/swartznet/contracts/dhtschema"
	"github.com/swartznet/swartznet/internal/dhtindex"
)

// options bundles the tunables so run() is testable without touching globals.
type options struct {
	stressN          int
	stressTimeout    time.Duration
	stressConcurrent int
	bootstrapTimeout time.Duration
	minGoodNodes     int
}

func main() {
	stressN := flag.Int("stress", 0, "after the smoke, run N concurrent BEP-44 Puts against the live DHT (0 = skip)")
	stressTimeout := flag.Duration("stress-timeout", 60*time.Second, "per-Put timeout during -stress")
	stressConcurrent := flag.Int("stress-concurrent", 8, "max concurrent Puts during -stress (<=0 = serial)")
	bootstrapTimeout := flag.Duration("bootstrap-timeout", 30*time.Second, "how long to wait for the routing table to populate")
	minGoodNodes := flag.Int("min-nodes", 8, "good-node target that ends the bootstrap wait early")
	flag.Usage = func() {
		w := flag.CommandLine.Output()
		fmt.Fprintln(w, "dht-smoke — live mainline-DHT smoke test for the SwartzNet Layer-D publisher path.")
		fmt.Fprintln(w, "\nBootstraps a DHT server, then (under a fresh ephemeral identity) Puts a synthetic")
		fmt.Fprintln(w, "keyword value over BEP-44 and Gets it back. Exit 0 = PASS, 1 = FAIL (no nodes, the")
		fmt.Fprintln(w, "smoke failed, or — with -stress — EVERY stress Put failed).\n\nFlags:")
		flag.PrintDefaults()
		fmt.Fprintln(w, "\nExamples:")
		fmt.Fprintln(w, "  dht-smoke                 # single Put/Get smoke test")
		fmt.Fprintln(w, "  dht-smoke -stress 20      # then 20 concurrent Puts + latency summary")
	}
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	os.Exit(run(context.Background(), log, os.Stderr, options{
		stressN:          *stressN,
		stressTimeout:    *stressTimeout,
		stressConcurrent: *stressConcurrent,
		bootstrapTimeout: *bootstrapTimeout,
		minGoodNodes:     *minGoodNodes,
	}))
}

// run performs the smoke (and optional stress) and returns a process exit code.
func run(ctx context.Context, log *slog.Logger, out io.Writer, opts options) int {
	srv, err := dht.NewServer(dht.NewDefaultServerConfig())
	if err != nil {
		fmt.Fprintln(out, "FAIL:", fmt.Errorf("dht.NewServer: %w", err))
		return 1
	}
	defer srv.Close()

	if _, err := srv.Bootstrap(); err != nil {
		log.Warn("dht.bootstrap_warn", "err", err)
	}
	waitForNodes(ctx, log, srv, opts.bootstrapTimeout, opts.minGoodNodes)
	if good := srv.Stats().GoodNodes; good < 1 {
		fmt.Fprintln(out, "FAIL: no good DHT nodes after bootstrap")
		return 1
	}

	// Fresh ephemeral identity — never the user's real publisher key. The goal
	// is to validate the wire path, not to leak an identity onto the live DHT.
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Fprintln(out, "FAIL:", fmt.Errorf("ed25519 keygen: %w", err))
		return 1
	}
	log.Info("identity.ephemeral", "pubkey_first_8", fmt.Sprintf("%x", pub[:8]))

	putter, err := dhtindex.NewAnacrolixPutter(srv, priv)
	if err != nil {
		fmt.Fprintln(out, "FAIL:", fmt.Errorf("NewAnacrolixPutter: %w", err))
		return 1
	}
	getter, err := dhtindex.NewAnacrolixGetter(srv)
	if err != nil {
		fmt.Fprintln(out, "FAIL:", fmt.Errorf("NewAnacrolixGetter: %w", err))
		return 1
	}

	var pk [32]byte
	copy(pk[:], pub)
	if err := smoke(ctx, log, putter, getter, pk); err != nil {
		fmt.Fprintln(out, "FAIL:", err)
		return 1
	}

	if opts.stressN > 0 {
		sum := runStress(ctx, log, putter, opts)
		logStress(log, sum)
		// THE fix: an all-failed stress phase is a hard failure. The legacy tool
		// logged this as a warning and still exited 0 — a dead DHT path must exit
		// non-zero. A partial failure is fine (it is the interesting measurement).
		if stressFailedHard(sum) {
			fmt.Fprintf(out, "FAIL: all %d stress Puts failed\n", sum.total)
			return 1
		}
	}

	fmt.Fprintln(out, "PASS")
	return 0
}

// waitForNodes blocks until the routing table has minGood good nodes or the
// timeout/ctx elapses, logging progress.
func waitForNodes(ctx context.Context, log *slog.Logger, srv *dht.Server, timeout time.Duration, minGood int) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		s := srv.Stats()
		log.Info("dht.bootstrap_progress", "good_nodes", s.GoodNodes, "nodes", s.Nodes, "outbound", s.OutboundQueriesAttempted)
		if s.GoodNodes >= minGood {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			return
		case <-tick.C:
		}
	}
}

// smoke Puts one synthetic keyword value and Gets it back, verifying the hit.
func smoke(ctx context.Context, log *slog.Logger, putter *dhtindex.AnacrolixPutter, getter *dhtindex.AnacrolixGetter, pk [32]byte) error {
	keyword := fmt.Sprintf("swartznet_smoke_%d", time.Now().UnixNano())
	salt, err := dhtschema.SaltForKeyword(keyword)
	if err != nil {
		return fmt.Errorf("SaltForKeyword: %w", err)
	}
	value := dhtschema.KeywordValue{Hits: []dhtschema.KeywordHit{{
		IH: bytes.Repeat([]byte{0xab}, 20), N: "smoke test", S: 1,
	}}}

	putCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	start := time.Now()
	if err := putter.Put(putCtx, salt, value); err != nil {
		return fmt.Errorf("put: %w", err)
	}
	log.Info("put.ok", "keyword", keyword, "elapsed", time.Since(start).String())

	getCtx, cancel2 := context.WithTimeout(ctx, 30*time.Second)
	defer cancel2()
	start = time.Now()
	got, err := getter.Get(getCtx, pk, salt)
	if err != nil {
		return fmt.Errorf("get: %w", err)
	}
	if len(got.Hits) != 1 {
		return fmt.Errorf("get returned %d hits, want 1", len(got.Hits))
	}
	if got.Hits[0].N != "smoke test" {
		return fmt.Errorf("get returned name %q, want %q", got.Hits[0].N, "smoke test")
	}
	log.Info("get.ok", "elapsed", time.Since(start).String(), "hits", len(got.Hits))
	return nil
}

// stressSummary is the aggregate outcome of the stress phase.
type stressSummary struct {
	total     int
	ok        int
	latencies []time.Duration // successful puts only, sorted ascending
	errBucket map[string]int
	wall      time.Duration
}

// runStress issues opts.stressN concurrent Puts (bounded by stressConcurrent)
// and returns the aggregated outcome.
func runStress(ctx context.Context, log *slog.Logger, putter *dhtindex.AnacrolixPutter, opts options) stressSummary {
	total := opts.stressN
	conc := opts.stressConcurrent
	if conc <= 0 {
		conc = 1
	}
	if conc > total {
		conc = total
	}
	log.Info("stress.start", "total_puts", total, "concurrency", conc, "per_put_timeout", opts.stressTimeout.String())

	type result struct {
		elapsed time.Duration
		err     error
	}
	results := make([]result, total)
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			keyword := fmt.Sprintf("swartznet_stress_%d_%d", time.Now().UnixNano(), idx)
			salt, err := dhtschema.SaltForKeyword(keyword)
			if err != nil {
				results[idx] = result{err: err}
				return
			}
			value := dhtschema.KeywordValue{Hits: []dhtschema.KeywordHit{{
				IH: bytes.Repeat([]byte{byte(idx)}, 20), N: fmt.Sprintf("stress hit %d", idx), S: 1,
			}}}
			pctx, cancel := context.WithTimeout(ctx, opts.stressTimeout)
			defer cancel()
			t0 := time.Now()
			err = putter.Put(pctx, salt, value)
			results[idx] = result{elapsed: time.Since(t0), err: err}
		}(i)
	}
	wg.Wait()

	sum := stressSummary{total: total, errBucket: map[string]int{}, wall: time.Since(start)}
	for _, r := range results {
		if r.err == nil {
			sum.ok++
			sum.latencies = append(sum.latencies, r.elapsed)
		} else {
			sum.errBucket[r.err.Error()]++
		}
	}
	sort.Slice(sum.latencies, func(i, j int) bool { return sum.latencies[i] < sum.latencies[j] })
	return sum
}

// stressFailedHard reports whether the stress phase is a HARD failure that must
// fail the exit code: puts were attempted and every one failed. A partial
// failure (ok > 0) is not a hard failure — it is the interesting measurement.
// This is the §6 fix in one place: the legacy tool never applied it, so a fully
// dead DHT path still exited 0.
func stressFailedHard(sum stressSummary) bool {
	return sum.total > 0 && sum.ok == 0
}

// logStress emits the stress summary + latency distribution.
func logStress(log *slog.Logger, s stressSummary) {
	rate := 0.0
	if s.total > 0 {
		rate = 100 * float64(s.ok) / float64(s.total)
	}
	log.Info("stress.summary", "total", s.total, "success", s.ok, "fail", s.total-s.ok,
		"success_rate", fmt.Sprintf("%.1f%%", rate), "wall_clock", s.wall.String())
	if len(s.latencies) > 0 {
		pick := func(p float64) time.Duration {
			return s.latencies[int(float64(len(s.latencies)-1)*p)]
		}
		log.Info("stress.latency", "min", s.latencies[0].String(), "p50", pick(0.50).String(),
			"p95", pick(0.95).String(), "max", s.latencies[len(s.latencies)-1].String())
	}
	for msg, n := range s.errBucket {
		log.Warn("stress.error_bucket", "count", n, "err", msg)
	}
}
