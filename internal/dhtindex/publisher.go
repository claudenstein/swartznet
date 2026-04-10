package dhtindex

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// PublishTask describes one torrent worth of work for the publisher
// worker. The worker tokenises the name, then publishes (or queues
// for refresh) one DHT entry per resulting keyword.
type PublishTask struct {
	InfoHash  []byte // 20-byte sha1 infohash of the torrent
	Name      string // human-readable name (gets tokenised)
	Seeders   int    // last known seeder count
	FileCount int    // file count in the torrent
	SizeBytes int64  // total bytes
}

// PublisherOptions tunes the publisher worker. The defaults are
// chosen so a fresh client behaves reasonably; tests override them
// to drive the worker faster.
type PublisherOptions struct {
	// RefreshInterval is how often the worker re-publishes every
	// entry in the manifest. BEP-44 expires items after 2h, so the
	// safe upper bound is ~1h. Default: 1h.
	RefreshInterval time.Duration
	// PutTimeout bounds a single Put traversal. Default: 30s.
	PutTimeout time.Duration
	// QueueSize is the buffered task channel capacity. Default: 64.
	QueueSize int
}

// DefaultPublisherOptions returns the production defaults.
func DefaultPublisherOptions() PublisherOptions {
	return PublisherOptions{
		RefreshInterval: 1 * time.Hour,
		PutTimeout:      30 * time.Second,
		QueueSize:       64,
	}
}

// Publisher is the long-running worker that owns the manifest and
// drives all DHT puts. Construct with NewPublisher, attach via the
// engine, and Stop on shutdown.
type Publisher struct {
	log      *slog.Logger
	put      Putter
	manifest *Manifest
	opts     PublisherOptions

	tasks    chan PublishTask
	stopOnce sync.Once
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// NewPublisher constructs a Publisher. log may be nil. opts is
// optional; pass DefaultPublisherOptions for production.
func NewPublisher(put Putter, manifest *Manifest, opts PublisherOptions, log *slog.Logger) *Publisher {
	if log == nil {
		log = slog.Default()
	}
	if opts.RefreshInterval <= 0 {
		opts.RefreshInterval = 1 * time.Hour
	}
	if opts.PutTimeout <= 0 {
		opts.PutTimeout = 30 * time.Second
	}
	if opts.QueueSize <= 0 {
		opts.QueueSize = 64
	}
	return &Publisher{
		log:      log,
		put:      put,
		manifest: manifest,
		opts:     opts,
		tasks:    make(chan PublishTask, opts.QueueSize),
		stopCh:   make(chan struct{}),
	}
}

// Start launches the worker goroutine. Call exactly once.
func (p *Publisher) Start() {
	p.wg.Add(1)
	go p.run()
}

// Stop signals the worker to drain its current task and exit, then
// waits for it. Idempotent. Persists the manifest one last time.
func (p *Publisher) Stop() {
	p.stopOnce.Do(func() {
		close(p.stopCh)
	})
	p.wg.Wait()
	if err := p.manifest.Save(); err != nil {
		p.log.Warn("dhtindex.publisher.save_on_stop_err", "err", err)
	}
}

// Submit enqueues one torrent for publication. Non-blocking; if the
// queue is full the task is dropped and a warning is logged. The
// dropped torrent will be picked up on the next refresh tick.
func (p *Publisher) Submit(task PublishTask) {
	select {
	case p.tasks <- task:
	default:
		p.log.Warn("dhtindex.publisher.queue_full",
			"infohash", task.InfoHash, "name", task.Name)
	}
}

// run is the worker loop. It services Submit() calls AND a refresh
// ticker that re-publishes every entry in the manifest on a regular
// schedule.
func (p *Publisher) run() {
	defer p.wg.Done()

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
			p.handleTask(task)
		case <-tick.C:
			p.refreshAll()
		}
	}
}

// handleTask tokenises the torrent name, updates the manifest for
// each keyword, and triggers a per-keyword publish.
func (p *Publisher) handleTask(task PublishTask) {
	if len(task.InfoHash) != 20 {
		p.log.Debug("dhtindex.publisher.bad_infohash", "len", len(task.InfoHash))
		return
	}
	keywords := Tokenize(task.Name)
	if len(keywords) == 0 {
		return
	}
	hit := KeywordHit{
		IH: append([]byte(nil), task.InfoHash...),
		N:  task.Name,
		S:  task.Seeders,
		F:  task.FileCount,
		Sz: task.SizeBytes,
	}
	for _, kw := range keywords {
		if _, err := p.manifest.AddHit(kw, hit); err != nil {
			p.log.Warn("dhtindex.publisher.add_hit_err", "kw", kw, "err", err)
			continue
		}
		p.publishOne(kw)
	}
	if err := p.manifest.Save(); err != nil {
		p.log.Warn("dhtindex.publisher.save_err", "err", err)
	}
}

// publishOne re-publishes the manifest entry for the given keyword.
// Errors are recorded on the manifest entry and logged but never
// returned — one bad keyword must not stop the worker.
func (p *Publisher) publishOne(keyword string) {
	snap := p.manifest.Snapshot()
	entry, ok := snap[keyword]
	if !ok {
		return
	}
	salt, err := SaltForKeyword(keyword)
	if err != nil {
		p.manifest.MarkFailed(keyword, err)
		return
	}
	value := KeywordValue{Hits: entry.Hits}
	ctx, cancel := context.WithTimeout(context.Background(), p.opts.PutTimeout)
	defer cancel()
	if err := p.put.Put(ctx, salt, value); err != nil {
		p.log.Warn("dhtindex.publisher.put_err",
			"keyword", keyword, "hits", len(entry.Hits), "err", err)
		p.manifest.MarkFailed(keyword, err)
		return
	}
	p.manifest.MarkPublished(keyword, time.Now())
	p.log.Debug("dhtindex.publisher.put_ok",
		"keyword", keyword, "hits", len(entry.Hits))
}

// refreshAll re-publishes every entry in the manifest. Called from
// the refresh ticker.
func (p *Publisher) refreshAll() {
	snap := p.manifest.Snapshot()
	for keyword := range snap {
		select {
		case <-p.stopCh:
			return
		default:
		}
		p.publishOne(keyword)
	}
	if err := p.manifest.Save(); err != nil {
		p.log.Warn("dhtindex.publisher.save_err_after_refresh", "err", err)
	}
}

// PublisherStatus returns a point-in-time view of the publisher
// state suitable for the M4f `swartznet publish status` command.
type PublisherStatus struct {
	TotalKeywords int
	TotalHits     int
	LastPublishes []PublisherKeywordStatus
}

// PublisherKeywordStatus is one row in the publisher status output.
type PublisherKeywordStatus struct {
	Keyword       string
	HitsCount     int
	LastPublished time.Time
	PublishCount  int
	LastError     string
}

// Status returns a snapshot of the manifest as a PublisherStatus.
func (p *Publisher) Status() PublisherStatus {
	snap := p.manifest.Snapshot()
	status := PublisherStatus{TotalKeywords: len(snap)}
	for kw, entry := range snap {
		status.TotalHits += len(entry.Hits)
		status.LastPublishes = append(status.LastPublishes, PublisherKeywordStatus{
			Keyword:       kw,
			HitsCount:     len(entry.Hits),
			LastPublished: entry.LastPublished,
			PublishCount:  entry.PublishCount,
			LastError:     entry.LastError,
		})
	}
	return status
}

// ErrPublisherClosed is returned by helpers that detect the
// Publisher has been Stopped. Currently unused; reserved for the
// engine wiring in M4d's second half.
var ErrPublisherClosed = errors.New("dhtindex: publisher closed")
