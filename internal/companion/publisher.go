package companion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// contentFingerprint hashes a companion index's CORPUS (its torrent records,
// which carry the extracted content) while ignoring the top-level GeneratedAt +
// Publisher fields. Per-torrent hashes are XORed so the result is independent of
// the torrent order the corpus source yields (infohashes are unique, so no two
// cancel out). Two rebuilds of the same corpus produce the same fingerprint.
func contentFingerprint(idx CompanionIndex) [32]byte {
	var fp [32]byte
	for _, tr := range idx.Torrents {
		raw, err := json.Marshal(tr)
		if err != nil {
			continue
		}
		h := sha256.Sum256(raw)
		for i := range fp {
			fp[i] ^= h[i]
		}
	}
	return fp
}

// ErrTooSoon is returned by RefreshNow when a manual refresh is throttled.
var ErrTooSoon = errors.New("companion: refresh throttled (too soon since last refresh)")

// PublisherOptions tunes the companion publisher.
type PublisherOptions struct {
	Dir          string        // on-disk dir for the payload + .torrent; REQUIRED
	PublisherKey [32]byte      // ed25519 pubkey: BEP-44 namespace + Publisher field
	Interval     time.Duration // rebuild+republish cadence; default 1h (≤ the 2h pointer TTL)
	MinInterval  time.Duration // throttle for manual RefreshNow; default 1m
	PutTimeout   time.Duration // bounds one BEP-44 put traversal; default 30s
	Build        BuildOptions  // what BuildFromIndex includes
}

// DefaultPublisherOptions returns the production defaults.
func DefaultPublisherOptions() PublisherOptions {
	return PublisherOptions{
		Interval:    1 * time.Hour,
		MinInterval: 1 * time.Minute,
		PutTimeout:  30 * time.Second,
		Build:       DefaultBuildOptions(),
	}
}

// RegtestPublisherOptions returns accelerated timings for scenario tests. NEVER
// production — a real node would hammer the mainline DHT.
func RegtestPublisherOptions() PublisherOptions {
	return PublisherOptions{
		Interval:    10 * time.Second,
		MinInterval: 100 * time.Millisecond,
		PutTimeout:  5 * time.Second,
		Build:       DefaultBuildOptions(),
	}
}

// Publisher rebuilds and republishes a companion index on a ticker. lastRefresh
// (the last SUCCESSFUL publish) advances ONLY on success, so it truthfully
// answers "is my pointer still alive (<2h)?".
type Publisher struct {
	src       CorpusSource
	putter    PointerPutter
	seeder    TorrentSeeder
	pubkeyHex string
	opts      PublisherOptions
	log       *slog.Logger

	mu             sync.Mutex
	lastRefresh    time.Time // last SUCCESSFUL publish
	lastAttempt    time.Time // last attempt of any outcome
	lastInfoHash   string
	lastError      string
	publishedCount int
	lastSeededIH   [20]byte // the companion seed torrent currently held (zero = none)
	// lastContentFP fingerprints the last-published CORPUS (torrents + content,
	// excluding the timestamp); lastGeneratedAt is the timestamp that went with
	// it. When a rebuild's content is unchanged, the publisher reuses that
	// timestamp so the payload — and thus the companion infohash — is byte-
	// identical, so followers' pointer dedup fires and they do not re-fetch +
	// re-ingest an unchanged snapshot every interval. The pointer is still re-put
	// (BEP-44 TTL refresh), just at the SAME infohash.
	lastContentFP   [32]byte
	lastGeneratedAt int64

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	trigger   chan struct{} // buffered cap 1
	wg        sync.WaitGroup
}

// NewPublisher validates its ports and defaults its options.
func NewPublisher(src CorpusSource, putter PointerPutter, seeder TorrentSeeder, opts PublisherOptions, log *slog.Logger) (*Publisher, error) {
	if src == nil {
		return nil, errors.New("companion: nil corpus source")
	}
	if putter == nil {
		return nil, errors.New("companion: nil putter")
	}
	if seeder == nil {
		return nil, errors.New("companion: nil seeder")
	}
	if opts.Dir == "" {
		return nil, errors.New("companion: empty dir")
	}
	if log == nil {
		log = slog.Default()
	}
	if opts.Interval <= 0 {
		opts.Interval = 1 * time.Hour
	}
	if opts.MinInterval <= 0 {
		opts.MinInterval = 1 * time.Minute
	}
	if opts.PutTimeout <= 0 {
		opts.PutTimeout = 30 * time.Second
	}
	return &Publisher{
		src:       src,
		putter:    putter,
		seeder:    seeder,
		pubkeyHex: hex.EncodeToString(opts.PublisherKey[:]),
		opts:      opts,
		log:       log,
		stopCh:    make(chan struct{}),
		trigger:   make(chan struct{}, 1),
	}, nil
}

// Start launches the worker goroutine (idempotent).
func (p *Publisher) Start() {
	p.startOnce.Do(func() {
		p.wg.Add(1)
		go p.run()
	})
}

// Stop signals the worker and waits for it (idempotent). Closing stopCh cancels
// the run-context so an in-flight put is cancelled rather than blocking Stop.
func (p *Publisher) Stop() {
	if p == nil {
		return
	}
	p.stopOnce.Do(func() { close(p.stopCh) })
	p.wg.Wait()
}

// RefreshNow requests an immediate rebuild, throttled by MinInterval measured
// against the last ATTEMPT (so a failed retry cannot masquerade as a recent
// successful publish).
func (p *Publisher) RefreshNow() error {
	p.mu.Lock()
	if !p.lastAttempt.IsZero() && time.Since(p.lastAttempt) < p.opts.MinInterval {
		p.mu.Unlock()
		return ErrTooSoon
	}
	p.mu.Unlock()
	select {
	case p.trigger <- struct{}{}:
	default:
	}
	return nil
}

func (p *Publisher) run() {
	defer p.wg.Done()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-p.stopCh
		cancel()
	}()
	p.refreshOnce(ctx) // immediate first build so the GUI shows something fast
	tick := time.NewTicker(p.opts.Interval)
	defer tick.Stop()
	for {
		select {
		case <-p.stopCh:
			return
		case <-tick.C:
			p.refreshOnce(ctx)
		case <-p.trigger:
			p.refreshOnce(ctx)
		}
	}
}

// refreshOnce runs the full pipeline once. Every failure is recorded + logged
// but never escalated — the worker keeps running.
func (p *Publisher) refreshOnce(parent context.Context) {
	idx, err := BuildFromIndex(p.src, p.pubkeyHex, p.opts.Build)
	if err != nil {
		p.recordFailure(fmt.Errorf("build: %w", err))
		return
	}
	// EMPTY = FAILURE: an empty local index is not a no-op success; lastRefresh
	// does not advance, and the next tick may find something.
	if len(idx.Torrents) == 0 {
		p.recordFailure(errors.New("nothing to publish (empty local index)"))
		return
	}
	// If the corpus is unchanged since the last publish, reuse the prior
	// timestamp so the payload (and infohash) is identical — the pointer still
	// re-publishes (TTL refresh) but at the SAME infohash, so followers do not
	// re-download + re-ingest the unchanged snapshot.
	fp := contentFingerprint(idx)
	p.mu.Lock()
	if fp == p.lastContentFP && p.lastGeneratedAt != 0 {
		idx.GeneratedAt = p.lastGeneratedAt
	} else {
		// Keep GeneratedAt strictly monotonic across content changes. Followers
		// reject a snapshot whose GeneratedAt regressed (replay defense), so if
		// the wall clock stepped backward (NTP correction, VM migration) we must
		// still advance past the last published timestamp — otherwise legitimate
		// new content would be silently dropped by every follower until the clock
		// caught back up. The bump only triggers under clock regression; normal
		// forward time already satisfies now > lastGeneratedAt.
		if p.lastGeneratedAt != 0 && idx.GeneratedAt <= p.lastGeneratedAt {
			idx.GeneratedAt = p.lastGeneratedAt + 1
		}
		p.lastContentFP = fp
		p.lastGeneratedAt = idx.GeneratedAt
	}
	p.mu.Unlock()

	jsonPath, mi, err := WriteCompanionFiles(p.opts.Dir, idx)
	if err != nil {
		p.recordFailure(fmt.Errorf("write: %w", err))
		return
	}
	// Seed errors are benign (anacrolix dedupes by infohash) — log at Debug and
	// continue to publish the pointer regardless. The single file is served
	// from where WriteCompanionFiles wrote it.
	if err := p.seeder.SeedMetaInfo(mi, jsonPath); err != nil {
		p.log.Debug("companion.publisher.seed_warn", "err", err)
	}
	infoHash := mi.HashInfoBytes()

	// Publish the pointer BEFORE dropping the previous seed. If the put fails, the
	// live BEP-46 pointer still resolves to the PREVIOUS infohash, so we must keep
	// seeding it — dropping it first would leave a new follower unable to fetch
	// the still-advertised (old) index until the next successful refresh.
	ctx, cancel := context.WithTimeout(parent, p.opts.PutTimeout)
	defer cancel()
	if err := p.putter.PutInfohashPointer(ctx, []byte(SaltContentIndex), infoHash); err != nil {
		p.recordFailure(fmt.Errorf("put pointer: %w", err))
		return
	}

	// Put succeeded — now it is safe to drop the previously-seeded companion
	// torrent (its infohash differs because the content changed) so companion
	// seeds do not accumulate in the engine over the node's lifetime.
	p.mu.Lock()
	prev := p.lastSeededIH
	p.lastSeededIH = [20]byte(infoHash)
	p.mu.Unlock()
	if prev != ([20]byte{}) && prev != [20]byte(infoHash) {
		if err := p.seeder.DropTorrent(prev); err != nil {
			p.log.Debug("companion.publisher.drop_warn", "err", err)
		}
	}
	p.recordSuccess(infoHash.HexString())
	p.log.Info("companion.publisher.refreshed", "infohash", infoHash.HexString(), "torrents", len(idx.Torrents))
}

func (p *Publisher) recordSuccess(infoHashHex string) {
	now := time.Now()
	p.mu.Lock()
	p.lastRefresh = now
	p.lastAttempt = now
	p.lastInfoHash = infoHashHex
	p.lastError = ""
	p.publishedCount++
	p.mu.Unlock()
}

func (p *Publisher) recordFailure(err error) {
	p.log.Warn("companion.publisher.refresh_failed", "err", err)
	p.mu.Lock()
	p.lastAttempt = time.Now()
	p.lastError = err.Error()
	p.mu.Unlock()
}

// PublisherStatus is a point-in-time view of the publisher.
type PublisherStatus struct {
	LastRefresh    time.Time // last SUCCESSFUL publish (zero until first success)
	LastAttempt    time.Time
	LastInfoHash   string
	LastError      string
	PublishedCount int
	PubKeyHex      string
}

// Status snapshots the publisher state.
func (p *Publisher) Status() PublisherStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return PublisherStatus{
		LastRefresh:    p.lastRefresh,
		LastAttempt:    p.lastAttempt,
		LastInfoHash:   p.lastInfoHash,
		LastError:      p.lastError,
		PublishedCount: p.publishedCount,
		PubKeyHex:      p.pubkeyHex,
	}
}
