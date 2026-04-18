package companion

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/swartznet/swartznet/internal/indexer"
)

// SaltContentIndex is the BEP-44 salt under which every
// SwartzNet publisher publishes their companion content-index
// pointer. Subscribers compute target = SHA1(pubkey ||
// SaltContentIndex) and issue a get against it. Stable
// constant; never change without bumping FormatVersion.
const SaltContentIndex = "_sn_content_index"

// PointerPutter is the narrow interface companion.Publisher
// needs from a BEP-44 mutable-item put implementation. The
// dhtindex.AnacrolixPutter satisfies it via PutInfohashPointer.
//
// Defined as a local interface so the companion package keeps
// no hard dependency on internal/dhtindex — same adapter
// pattern as everywhere else in the codebase.
type PointerPutter interface {
	PutInfohashPointer(ctx context.Context, salt []byte, infohash [20]byte) error
}

// TorrentSeeder is the narrow interface companion.Publisher
// needs from the engine in order to seed the freshly-built
// companion torrent. The engine's AddTorrentMetaInfo method
// satisfies it; the publisher does not need a typed handle
// back.
type TorrentSeeder interface {
	AddTorrentMetaInfo(mi *metainfo.MetaInfo) (any, error)
}

// PublisherOptions tunes how the periodic worker behaves. The
// defaults are sensible for a normal desktop install.
type PublisherOptions struct {
	// Dir is the on-disk directory for the companion files
	// (the JSON.gz payload and the wrapping .torrent). Required.
	Dir string
	// PublisherKey is the publisher's ed25519 pubkey. Used as
	// the BEP-44 namespace for the pointer (target =
	// SHA1(PublisherKey || SaltContentIndex)) and as the
	// "publisher" field on the JSON document.
	PublisherKey [32]byte
	// Interval is how often the worker rebuilds the companion
	// index and re-publishes the pointer. Default: 1h. The
	// pointer expires after 2h per BEP-44 so the interval
	// must be ≤ 1h to keep it alive.
	Interval time.Duration
	// MinInterval throttles how often a manual RefreshNow can
	// fire. Useful so a UI button doesn't let the user spam
	// the DHT. Default: 1 minute.
	MinInterval time.Duration
	// PutTimeout bounds a single BEP-44 put traversal.
	// Default: 30s.
	PutTimeout time.Duration
	// Build controls what BuildFromIndex includes. Default:
	// DefaultBuildOptions().
	Build BuildOptions
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

// RegtestPublisherOptions returns the accelerated options used
// in regtest mode. Same philosophy as
// dhtindex.RegtestPublisherOptions — shrink every production
// time constant so scenario tests run in seconds, not hours.
// NEVER use this in production.
func RegtestPublisherOptions() PublisherOptions {
	return PublisherOptions{
		Interval:    10 * time.Second,
		MinInterval: 100 * time.Millisecond,
		PutTimeout:  5 * time.Second,
		Build:       DefaultBuildOptions(),
	}
}

// Publisher is the long-running worker that owns the F3
// companion-index publication path:
//
//  1. Walk the local Bleve index to build a CompanionIndex.
//  2. Encode + write to disk as
//     <Dir>/swartznet-content-index-v1.json.gz.
//  3. Wrap it in a v1 .torrent metainfo and write
//     <Dir>/companion.torrent.
//  4. Add the metainfo to the engine so it gets seeded.
//  5. Publish a BEP-44 mutable item containing the new
//     infohash at salt SaltContentIndex.
//
// Refresh schedule: every PublisherOptions.Interval (default
// 1h). Manual triggers via RefreshNow are throttled by
// MinInterval. The current pointer infohash and last refresh
// time are exposed via Status() so the GUI / status command
// can show them.
//
// Concurrent-safe; Start launches one goroutine, Stop tears it
// down idempotently.
type Publisher struct {
	idx       *indexer.Index
	putter    PointerPutter
	seeder    TorrentSeeder
	pubkeyHex string
	opts      PublisherOptions
	log       *slog.Logger

	mu             sync.Mutex
	lastRefresh    time.Time
	lastInfoHash   string
	lastError      string
	publishedCount int

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	trigger   chan struct{}
	wg        sync.WaitGroup
}

// NewPublisher constructs a Publisher. Returns an error for
// nil collaborators or an empty Dir; everything else is
// validated lazily on the first refresh.
func NewPublisher(
	idx *indexer.Index,
	putter PointerPutter,
	seeder TorrentSeeder,
	opts PublisherOptions,
	log *slog.Logger,
) (*Publisher, error) {
	if idx == nil {
		return nil, errors.New("companion: nil index")
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
	pubkeyHex := hexEncode(opts.PublisherKey[:])
	return &Publisher{
		idx:       idx,
		putter:    putter,
		seeder:    seeder,
		pubkeyHex: pubkeyHex,
		opts:      opts,
		log:       log,
		stopCh:    make(chan struct{}),
		trigger:   make(chan struct{}, 1),
	}, nil
}

// Start launches the worker goroutine. Idempotent — subsequent
// calls are no-ops (guarded by a sync.Once).
func (p *Publisher) Start() {
	p.startOnce.Do(func() {
		p.wg.Add(1)
		go p.run()
	})
}

// Stop signals the worker to finish its current refresh and
// exit, then waits for it. Idempotent.
func (p *Publisher) Stop() {
	p.stopOnce.Do(func() {
		close(p.stopCh)
	})
	p.wg.Wait()
}

// RefreshNow asks the worker to perform an immediate refresh
// out-of-band. Subject to MinInterval throttling — if a
// refresh ran recently, this returns ErrTooSoon and the
// scheduled refresh tick handles things normally.
func (p *Publisher) RefreshNow() error {
	p.mu.Lock()
	if !p.lastRefresh.IsZero() && time.Since(p.lastRefresh) < p.opts.MinInterval {
		p.mu.Unlock()
		return ErrTooSoon
	}
	p.mu.Unlock()
	select {
	case p.trigger <- struct{}{}:
		return nil
	default:
		// A trigger is already queued; that's good enough.
		return nil
	}
}

// ErrTooSoon is returned by RefreshNow when called within
// PublisherOptions.MinInterval of the previous refresh.
var ErrTooSoon = errors.New("companion: refresh throttled (too soon since last refresh)")

// Status is the publisher's view of its own state, suitable
// for /status output.
type PublisherStatus struct {
	LastRefresh    time.Time
	LastInfoHash   string
	LastError      string
	PublishedCount int
	PubKeyHex      string
}

// Status returns a snapshot of the publisher's state.
func (p *Publisher) Status() PublisherStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return PublisherStatus{
		LastRefresh:    p.lastRefresh,
		LastInfoHash:   p.lastInfoHash,
		LastError:      p.lastError,
		PublishedCount: p.publishedCount,
		PubKeyHex:      p.pubkeyHex,
	}
}

// run is the worker goroutine. It services the refresh ticker
// and out-of-band RefreshNow triggers until Stop is called.
func (p *Publisher) run() {
	defer p.wg.Done()

	// Run an initial refresh as soon as the worker starts so
	// the GUI does not have to wait an hour to see anything.
	p.refreshOnce()

	tick := time.NewTicker(p.opts.Interval)
	defer tick.Stop()
	for {
		select {
		case <-p.stopCh:
			return
		case <-tick.C:
			p.refreshOnce()
		case <-p.trigger:
			p.refreshOnce()
		}
	}
}

// refreshOnce runs the full publish pipeline once: build →
// write → seed → put pointer. Failures at any step are
// recorded on the publisher state and logged but never
// escalated.
func (p *Publisher) refreshOnce() {
	idx, err := BuildFromIndex(p.idx, p.pubkeyHex, p.opts.Build)
	if err != nil {
		p.recordFailure(fmt.Errorf("build: %w", err))
		return
	}
	if len(idx.Torrents) == 0 {
		// Nothing indexed yet — publish nothing. The next
		// refresh tick may find something.
		p.recordFailure(errors.New("nothing to publish (empty local index)"))
		return
	}

	_, mi, err := WriteCompanionFiles(p.opts.Dir, idx)
	if err != nil {
		p.recordFailure(fmt.Errorf("write: %w", err))
		return
	}

	if _, err := p.seeder.AddTorrentMetaInfo(mi); err != nil {
		// Re-adding the same metainfo is benign (anacrolix
		// dedupes by infohash internally), so swallow that
		// error rather than treating it as a failure.
		p.log.Debug("companion.publisher.seed_warn", "err", err)
	}

	infoHash := mi.HashInfoBytes()
	ctx, cancel := context.WithTimeout(context.Background(), p.opts.PutTimeout)
	defer cancel()
	if err := p.putter.PutInfohashPointer(ctx, []byte(SaltContentIndex), infoHash); err != nil {
		p.recordFailure(fmt.Errorf("put pointer: %w", err))
		return
	}

	p.recordSuccess(infoHash.HexString())
	p.log.Info("companion.publisher.refreshed",
		"infohash", infoHash.HexString(),
		"torrents", len(idx.Torrents),
	)
}

func (p *Publisher) recordSuccess(infoHashHex string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastRefresh = time.Now()
	p.lastInfoHash = infoHashHex
	p.lastError = ""
	p.publishedCount++
}

func (p *Publisher) recordFailure(err error) {
	p.log.Warn("companion.publisher.refresh_failed", "err", err)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastRefresh = time.Now()
	p.lastError = err.Error()
}

// hexEncode is a tiny local hex helper that avoids importing
// the encoding/hex package. The pubkey is exactly 32 bytes so
// the output is exactly 64 chars.
func hexEncode(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, x := range b {
		out[i*2] = digits[x>>4]
		out[i*2+1] = digits[x&0x0f]
	}
	return string(out)
}

// _ ensures we use these imports even if compile-time
// reflection elides one of them on a refactor.
var _ = sha1.Sum
var _ = bencode.Marshal
