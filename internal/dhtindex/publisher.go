package dhtindex

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/swartznet/swartznet/contracts/dhtschema"
	"github.com/swartznet/swartznet/contracts/token"
)

// PublishTask is one torrent worth of work: the worker tokenizes the NAME
// (never content) and publishes one keyword entry per resulting token.
type PublishTask struct {
	InfoHash  []byte // 20-byte sha1 infohash
	Name      string // human-readable name (tokenized to keywords)
	Seeders   int
	FileCount int
	SizeBytes int64
}

// PublisherOptions tunes the worker and the backend beneath it. RefreshInterval
// and QueueSize drive the worker; PutTimeout and MinPutInterval drive the
// legacyKeyword backend. These are code-owned constants (Default vs Regtest),
// deliberately NOT user config flags.
type PublisherOptions struct {
	// RefreshInterval re-announces every entry (BEP-44 expires at 2h). Default 1h.
	RefreshInterval time.Duration
	// PutTimeout bounds a single Put traversal. Default 30s.
	PutTimeout time.Duration
	// QueueSize is the buffered task channel capacity. Default 64.
	QueueSize int
	// MinPutInterval is the hard per-keyword publish budget: a keyword put
	// less than this long ago is skipped. Default 55m (just under
	// RefreshInterval, leaving skew budget before the 2h TTL). Zero disables.
	MinPutInterval time.Duration
}

// DefaultPublisherOptions returns the production defaults.
func DefaultPublisherOptions() PublisherOptions {
	return PublisherOptions{
		RefreshInterval: 1 * time.Hour,
		PutTimeout:      30 * time.Second,
		QueueSize:       64,
		MinPutInterval:  55 * time.Minute,
	}
}

// RegtestPublisherOptions returns the accelerated regtest options so
// scenario tests that turn on "after the next refresh" run in seconds. NEVER
// production: a real node running these would hammer the mainline DHT. The
// engine logs a prominent warning when Config.Regtest is set.
func RegtestPublisherOptions() PublisherOptions {
	return PublisherOptions{
		RefreshInterval: 5 * time.Second,
		PutTimeout:      5 * time.Second,
		QueueSize:       64,
		MinPutInterval:  100 * time.Millisecond,
	}
}

// Publisher is the long-running worker that drives a RecordBackend: it services
// Submit() calls and a refresh ticker. It is a thin scheduling shell — format-
// specific persistence, throttle, and eviction all live inside the backend.
type Publisher struct {
	log     *slog.Logger
	backend RecordBackend
	opts    PublisherOptions

	tasks    chan PublishTask
	stopOnce sync.Once
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// NewPublisher constructs a worker over backend. log may be nil.
func NewPublisher(backend RecordBackend, opts PublisherOptions, log *slog.Logger) *Publisher {
	if log == nil {
		log = slog.Default()
	}
	if opts.RefreshInterval <= 0 {
		opts.RefreshInterval = 1 * time.Hour
	}
	if opts.QueueSize <= 0 {
		opts.QueueSize = 64
	}
	return &Publisher{
		log:     log,
		backend: backend,
		opts:    opts,
		tasks:   make(chan PublishTask, opts.QueueSize),
		stopCh:  make(chan struct{}),
	}
}

// Start launches the worker goroutine. Call exactly once.
func (p *Publisher) Start() {
	p.wg.Add(1)
	go p.run()
}

// Stop signals the worker to drain its current task and exit, waits for it,
// then closes the backend (final persistence). Idempotent.
func (p *Publisher) Stop() {
	if p == nil {
		return
	}
	p.stopOnce.Do(func() { close(p.stopCh) })
	p.wg.Wait()
	if err := p.backend.Close(); err != nil {
		p.log.Warn("dhtindex.publisher.close_err", "err", err)
	}
}

// Submit enqueues a torrent for publication. Non-blocking; a full queue drops
// the task with a warning (the next refresh tick will still pick it up if it
// reached the manifest).
func (p *Publisher) Submit(task PublishTask) {
	if p == nil {
		return
	}
	select {
	case p.tasks <- task:
	default:
		p.log.Warn("dhtindex.publisher.queue_full", "infohash", task.InfoHash, "name", task.Name)
	}
}

// Retract scrubs an infohash from everything the backend published. Safe from
// any goroutine; no-op on a nil publisher.
func (p *Publisher) Retract(ih [20]byte) {
	if p == nil {
		return
	}
	if err := p.backend.Retract(context.Background(), ih); err != nil {
		p.log.Warn("dhtindex.publisher.retract_err", "err", err)
	}
}

// Status returns the backend's point-in-time publish state.
func (p *Publisher) Status() PublisherStatus {
	if p == nil {
		return PublisherStatus{}
	}
	return p.backend.Status()
}

// run is the worker loop: Submit() tasks plus a refresh ticker.
func (p *Publisher) run() {
	defer p.wg.Done()
	// A context tied to stopCh cancels any in-flight put the moment Stop is
	// called, rather than holding the worker for the full PutTimeout.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-p.stopCh
		cancel()
	}()

	tick := time.NewTicker(p.opts.RefreshInterval)
	defer tick.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case task, ok := <-p.tasks:
			if !ok {
				return
			}
			p.handleTask(ctx, task)
		case <-tick.C:
			if err := p.backend.Refresh(ctx); err != nil && ctx.Err() == nil {
				p.log.Warn("dhtindex.publisher.refresh_err", "err", err)
			}
		}
	}
}

// handleTask tokenizes the torrent NAME (name-only invariant — content tokens
// never reach Layer D) and hands the ≤8 keywords plus the hit to the backend.
func (p *Publisher) handleTask(ctx context.Context, task PublishTask) {
	if len(task.InfoHash) != 20 {
		p.log.Debug("dhtindex.publisher.bad_infohash", "len", len(task.InfoHash))
		return
	}
	keywords := token.Tokenize(task.Name)
	if len(keywords) == 0 {
		return
	}
	hit := dhtschema.KeywordHit{
		IH: append([]byte(nil), task.InfoHash...),
		N:  task.Name,
		S:  task.Seeders,
		F:  task.FileCount,
		Sz: task.SizeBytes,
	}
	if err := p.backend.Publish(ctx, keywords, hit); err != nil && ctx.Err() == nil {
		p.log.Warn("dhtindex.publisher.publish_err", "err", err)
	}
}
