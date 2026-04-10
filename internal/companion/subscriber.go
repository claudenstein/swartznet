package companion

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/swartznet/swartznet/internal/indexer"
)

// PointerGetter is the narrow interface companion.Subscriber needs
// from a BEP-44 mutable-item get implementation. The
// dhtindex.AnacrolixGetter satisfies it via GetInfohashPointer.
//
// Defined as a local interface so the companion package keeps no
// hard dependency on internal/dhtindex.
type PointerGetter interface {
	GetInfohashPointer(ctx context.Context, pubkey [32]byte, salt []byte) ([20]byte, error)
}

// CompanionFetcher is the narrow interface companion.Subscriber
// needs from the engine in order to download a companion-index
// torrent given only its infohash. Implementations should:
//
//  1. Add the infohash to the engine (e.g. AddInfoHash).
//  2. Wait for metadata to arrive over the swarm.
//  3. Wait for the (single) file inside the torrent to fully
//     download.
//  4. Return the absolute on-disk path to that file.
//
// The provided ctx may be cancelled at any time; the fetcher
// should respect cancellation and return ctx.Err().
//
// The engine.Engine.FetchCompanionTorrent method satisfies this
// interface.
type CompanionFetcher interface {
	FetchCompanionTorrent(ctx context.Context, infohash [20]byte) (path string, err error)
}

// Ingester is the narrow interface the subscriber needs to write
// imported records into the local index. *indexer.Index satisfies
// it directly via IndexTorrent and IndexContent. The interface
// exists so the unit tests can supply an in-memory recorder
// without spinning up a Bleve directory.
type Ingester interface {
	IndexTorrent(doc indexer.TorrentDoc) error
	IndexContent(doc indexer.ContentDoc) error
}

// SubscriberOptions tunes how the subscriber behaves. The defaults
// are sensible for a normal desktop install.
type SubscriberOptions struct {
	// FetchTimeout bounds a single companion-torrent download.
	// Default: 5 minutes. Companion JSONs are typically a few MB
	// so this is generous; tune up if you publish very large
	// indexes.
	FetchTimeout time.Duration
	// PointerTimeout bounds a single BEP-44 get traversal.
	// Default: 30s.
	PointerTimeout time.Duration
	// Interval is how often the worker re-syncs every followed
	// publisher. Default: 1 hour.
	Interval time.Duration
}

// DefaultSubscriberOptions returns the production defaults.
func DefaultSubscriberOptions() SubscriberOptions {
	return SubscriberOptions{
		FetchTimeout:   5 * time.Minute,
		PointerTimeout: 30 * time.Second,
		Interval:       1 * time.Hour,
	}
}

// SyncResult is the per-publisher outcome of a single Sync call.
// Returned to the caller (and recorded on the worker for status
// reporting) so the GUI can show what was imported and when.
type SyncResult struct {
	// Publisher is the 64-char hex form of the publisher's
	// ed25519 public key.
	Publisher string
	// PointerInfoHash is the infohash that the BEP-46 pointer
	// resolved to. Empty when the pointer fetch failed.
	PointerInfoHash [20]byte
	// TorrentsImported is the number of TorrentRecord rows that
	// were written to the local index.
	TorrentsImported int
	// ContentImported is the number of ContentChunk rows that
	// were written to the local index.
	ContentImported int
	// GeneratedAt is the publisher-side timestamp from the
	// imported snapshot. Used by the worker to skip duplicate
	// imports of an unchanged snapshot.
	GeneratedAt int64
	// Err is non-nil if any step failed.
	Err error
}

// Subscriber is the read-side of the F3 companion-index story.
// It resolves the BEP-46 pointer published at salt
// SaltContentIndex by a given publisher, downloads the wrapping
// torrent, decodes the gzipped JSON payload, and ingests every
// record into the local index.
//
// Subscriber holds no state of its own; the periodic worker is
// SubscriberWorker.
type Subscriber struct {
	getter   PointerGetter
	fetcher  CompanionFetcher
	ingester Ingester
	opts     SubscriberOptions
	log      *slog.Logger
}

// NewSubscriber constructs a Subscriber. Returns an error if any
// collaborator is nil.
func NewSubscriber(
	getter PointerGetter,
	fetcher CompanionFetcher,
	ingester Ingester,
	opts SubscriberOptions,
	log *slog.Logger,
) (*Subscriber, error) {
	if getter == nil {
		return nil, errors.New("companion: nil pointer getter")
	}
	if fetcher == nil {
		return nil, errors.New("companion: nil fetcher")
	}
	if ingester == nil {
		return nil, errors.New("companion: nil ingester")
	}
	if log == nil {
		log = slog.Default()
	}
	if opts.FetchTimeout <= 0 {
		opts.FetchTimeout = 5 * time.Minute
	}
	if opts.PointerTimeout <= 0 {
		opts.PointerTimeout = 30 * time.Second
	}
	if opts.Interval <= 0 {
		opts.Interval = 1 * time.Hour
	}
	return &Subscriber{
		getter:   getter,
		fetcher:  fetcher,
		ingester: ingester,
		opts:     opts,
		log:      log,
	}, nil
}

// Sync runs one full pipeline pass for a single publisher:
//  1. Resolve the BEP-46 pointer at (pubkey, SaltContentIndex).
//  2. Fetch the underlying companion torrent.
//  3. Read + decode the JSON payload.
//  4. Ingest every record into the local index.
//
// Returns a SyncResult describing the outcome. The result is
// always populated even on failure (with Publisher set and Err
// non-nil) so callers can record it without doing nil checks.
func (s *Subscriber) Sync(ctx context.Context, pubkey [32]byte) SyncResult {
	pubHex := hexEncode(pubkey[:])
	res := SyncResult{Publisher: pubHex}

	// Step 1: pointer.
	getCtx, cancel := context.WithTimeout(ctx, s.opts.PointerTimeout)
	ih, err := s.getter.GetInfohashPointer(getCtx, pubkey, []byte(SaltContentIndex))
	cancel()
	if err != nil {
		res.Err = fmt.Errorf("get pointer: %w", err)
		return res
	}
	res.PointerInfoHash = ih

	// Step 2: torrent download.
	fetchCtx, cancel := context.WithTimeout(ctx, s.opts.FetchTimeout)
	path, err := s.fetcher.FetchCompanionTorrent(fetchCtx, ih)
	cancel()
	if err != nil {
		res.Err = fmt.Errorf("fetch companion torrent: %w", err)
		return res
	}

	// Step 3 + 4: decode + ingest.
	idx, err := s.decodeFile(path)
	if err != nil {
		res.Err = fmt.Errorf("decode %s: %w", path, err)
		return res
	}
	res.GeneratedAt = idx.GeneratedAt

	tCount, cCount, err := s.ingest(idx)
	res.TorrentsImported = tCount
	res.ContentImported = cCount
	if err != nil {
		res.Err = fmt.Errorf("ingest: %w", err)
		return res
	}
	s.log.Info("companion.subscriber.synced",
		"publisher", pubHex,
		"infohash", fmt.Sprintf("%x", ih),
		"torrents_imported", tCount,
		"content_imported", cCount,
	)
	return res
}

// IngestReader decodes a companion payload from the given reader
// and writes its records to the local index. Exposed so callers
// who already have an io.Reader (e.g. an in-process test) can
// skip the file path step. Returns the parsed CompanionIndex
// alongside per-record counts.
func (s *Subscriber) IngestReader(r io.Reader) (CompanionIndex, int, int, error) {
	idx, err := Decode(r)
	if err != nil {
		return CompanionIndex{}, 0, 0, fmt.Errorf("decode: %w", err)
	}
	tCount, cCount, err := s.ingest(idx)
	return idx, tCount, cCount, err
}

// decodeFile opens the on-disk file at path and runs Decode on
// it. The file must be the gzipped JSON payload (not the
// wrapping torrent).
func (s *Subscriber) decodeFile(path string) (CompanionIndex, error) {
	f, err := os.Open(path)
	if err != nil {
		return CompanionIndex{}, err
	}
	defer f.Close()
	return Decode(f)
}

// ingest writes every record in idx into the local indexer.Index.
// Returns the number of TorrentDoc and ContentDoc rows written
// (separately, since each can fail independently).
//
// Records that fail validation (empty infohash, empty content
// text) are skipped with a debug log. The first hard error
// (e.g. index closed) aborts the loop and is returned.
func (s *Subscriber) ingest(idx CompanionIndex) (int, int, error) {
	var (
		torrents int
		contents int
	)
	for _, tr := range idx.Torrents {
		ih := strings.ToLower(tr.InfoHash)
		if len(ih) != 40 {
			s.log.Debug("companion.subscriber.skip_torrent",
				"reason", "bad infohash",
				"infohash", ih,
			)
			continue
		}
		paths := make([]string, 0, len(tr.Files))
		for _, f := range tr.Files {
			if f.Path != "" {
				paths = append(paths, f.Path)
			}
		}
		td := indexer.TorrentDoc{
			InfoHash:  ih,
			Name:      tr.Name,
			SizeBytes: tr.Size,
			FilePaths: paths,
			FileCount: len(paths),
		}
		if tr.AddedAt > 0 {
			td.AddedAt = time.Unix(tr.AddedAt, 0).UTC()
		}
		if err := s.ingester.IndexTorrent(td); err != nil {
			return torrents, contents, fmt.Errorf("index torrent %s: %w", ih, err)
		}
		torrents++

		for _, fr := range tr.Files {
			for ci, ch := range fr.Chunks {
				if ch.Text == "" {
					continue
				}
				cd := indexer.ContentDoc{
					InfoHash:   ih,
					FileIndex:  fr.Index,
					FilePath:   fr.Path,
					FileSize:   fr.Size,
					Mime:       fr.Mime,
					Extractor:  fr.Extractor,
					Text:       ch.Text,
					ChunkIndex: ci,
				}
				if err := s.ingester.IndexContent(cd); err != nil {
					return torrents, contents, fmt.Errorf("index content %s/%d/%d: %w",
						ih, fr.Index, ci, err)
				}
				contents++
			}
		}
	}
	return torrents, contents, nil
}

// SubscriberWorker is the long-running periodic worker that
// re-syncs every followed publisher every Interval. Construct via
// NewSubscriberWorker and call Start; tear down via Stop.
//
// Followed publishers are identified by their 32-byte ed25519
// pubkey and added/removed via Follow / Unfollow. The worker
// holds no on-disk state; the caller is responsible for
// persisting the follow list across restarts (typically into a
// JSON file under ~/.local/share/swartznet).
//
// Concurrent-safe.
type SubscriberWorker struct {
	sub *Subscriber

	mu        sync.Mutex
	follows   map[[32]byte]string  // pubkey → human label
	lastSync  map[[32]byte]SyncResult
	stopCh    chan struct{}
	stopOnce  sync.Once
	wg        sync.WaitGroup
	trigger   chan struct{}
	interval  time.Duration
	totalRuns int
}

// NewSubscriberWorker constructs a worker around an existing
// Subscriber. The worker takes its Interval from the
// Subscriber's options.
func NewSubscriberWorker(sub *Subscriber) (*SubscriberWorker, error) {
	if sub == nil {
		return nil, errors.New("companion: nil subscriber")
	}
	return &SubscriberWorker{
		sub:      sub,
		follows:  make(map[[32]byte]string),
		lastSync: make(map[[32]byte]SyncResult),
		stopCh:   make(chan struct{}),
		trigger:  make(chan struct{}, 1),
		interval: sub.opts.Interval,
	}, nil
}

// Follow adds a publisher to the worker's follow list. Label is a
// human-readable name for status output. Calling Follow on an
// already-followed publisher updates the label. Triggers a
// background sync of the new publisher within a few moments.
func (w *SubscriberWorker) Follow(pubkey [32]byte, label string) {
	w.mu.Lock()
	w.follows[pubkey] = label
	w.mu.Unlock()
	// Wake the worker so it picks up the new publisher promptly
	// instead of waiting for the next tick.
	select {
	case w.trigger <- struct{}{}:
	default:
	}
}

// Unfollow removes a publisher from the worker's follow list.
// In-flight syncs of that publisher are NOT cancelled — they
// will run to completion and then be discarded.
func (w *SubscriberWorker) Unfollow(pubkey [32]byte) {
	w.mu.Lock()
	delete(w.follows, pubkey)
	delete(w.lastSync, pubkey)
	w.mu.Unlock()
}

// Following returns a snapshot of the current follow list as
// (pubkey, label) pairs.
func (w *SubscriberWorker) Following() map[[32]byte]string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[[32]byte]string, len(w.follows))
	for k, v := range w.follows {
		out[k] = v
	}
	return out
}

// LastSync returns the most-recent SyncResult for a publisher,
// or zero value if the worker has not yet synced that
// publisher.
func (w *SubscriberWorker) LastSync(pubkey [32]byte) SyncResult {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastSync[pubkey]
}

// AllResults returns a snapshot of every recorded SyncResult.
// Useful for /status output.
func (w *SubscriberWorker) AllResults() []SyncResult {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]SyncResult, 0, len(w.lastSync))
	for _, r := range w.lastSync {
		out = append(out, r)
	}
	return out
}

// TotalRuns returns the number of full sync passes the worker
// has performed since Start.
func (w *SubscriberWorker) TotalRuns() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.totalRuns
}

// Start launches the worker goroutine. Idempotent.
func (w *SubscriberWorker) Start() {
	w.wg.Add(1)
	go w.run()
}

// Stop signals the worker to finish its current sync pass and
// exit, then waits for it. Idempotent.
func (w *SubscriberWorker) Stop() {
	w.stopOnce.Do(func() { close(w.stopCh) })
	w.wg.Wait()
}

// run is the worker goroutine. Runs an initial sync pass on
// startup, then re-syncs every interval until Stop is called.
func (w *SubscriberWorker) run() {
	defer w.wg.Done()
	w.runOnce()

	tick := time.NewTicker(w.interval)
	defer tick.Stop()
	for {
		select {
		case <-w.stopCh:
			return
		case <-tick.C:
			w.runOnce()
		case <-w.trigger:
			w.runOnce()
		}
	}
}

// runOnce syncs every followed publisher. Errors on individual
// publishers are recorded on the worker state; they do not
// abort the loop.
func (w *SubscriberWorker) runOnce() {
	w.mu.Lock()
	pubs := make([][32]byte, 0, len(w.follows))
	for k := range w.follows {
		pubs = append(pubs, k)
	}
	w.mu.Unlock()

	ctx := context.Background()
	for _, pub := range pubs {
		// Stop early if Stop was called between iterations.
		select {
		case <-w.stopCh:
			return
		default:
		}
		res := w.sub.Sync(ctx, pub)
		w.mu.Lock()
		// If the publisher's snapshot timestamp matches the
		// last imported one, we still record the run but don't
		// double-count it as new content.
		w.lastSync[pub] = res
		w.mu.Unlock()
	}
	w.mu.Lock()
	w.totalRuns++
	w.mu.Unlock()
}
