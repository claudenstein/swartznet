package indexer

import (
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/swartznet/swartznet/internal/indexer/extractors"
)

// Pipeline is the side of the indexer that consumes file-complete events,
// runs a text extractor, and writes the result to the index as a
// ContentDoc. It is intentionally decoupled from the Engine: the only
// thing it needs is a stream of FileInput structs plus the *Index to
// write into.
//
// The Engine is responsible for producing FileInput values from its
// FileCompleteEvent stream (adding the actual file Reader, which only
// the live torrent knows how to construct). Pipeline itself knows
// nothing about anacrolix/torrent.
type Pipeline struct {
	log          *slog.Logger
	idx          *Index
	maxFileBytes int64 // per-file extract cap; 0 = extractor default

	input chan FileInput

	wg        sync.WaitGroup
	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}

	// counters tracks per-infohash extraction progress. Keys are
	// lowercased hex infohashes, values are *ihCounters. Populated
	// lazily on first Submit for a given infohash.
	counters sync.Map
}

// ihCounters is the set of atomic tallies kept for a single torrent.
// Processed advances past every file the pipeline has finished with,
// whether it produced chunks, was skipped by dispatch, or errored.
type ihCounters struct {
	processed atomic.Int64
	extracted atomic.Int64
	skipped   atomic.Int64
	failed    atomic.Int64
}

// PipelineStats is a snapshot of the extraction counters for one
// torrent. Zero-valued when the infohash has never been submitted.
type PipelineStats struct {
	// Processed is the total count of files the pipeline has
	// finished handling. Equals Extracted + Skipped + Failed.
	Processed int64
	// Extracted is the count of files that yielded at least one
	// content chunk (the useful work).
	Extracted int64
	// Skipped is the count of files where no extractor matched
	// or extraction returned an empty chunk list. Normal for
	// images, videos, archives, etc.
	Skipped int64
	// Failed is the count of files where the extractor returned
	// an error or the index-write failed for every chunk.
	Failed int64
}

// FileInput describes a single completed file ready for extraction.
// The Reader is provided lazily via OpenReader so the pipeline can decide
// whether to extract at all (based on size, MIME, reputation in later
// milestones) before touching disk.
type FileInput struct {
	InfoHash  string
	FileIndex int
	Path      string
	Size      int64

	// OpenReader returns a reader for the file contents. The pipeline
	// is responsible for closing it (if it implements io.Closer).
	OpenReader func() (io.Reader, error)
}

// NewPipeline constructs a Pipeline with the given index and logger.
// maxFileBytes is the extractor's per-file byte cap; zero means "use the
// extractor's default". Start must be called before any input is queued.
func NewPipeline(idx *Index, log *slog.Logger, maxFileBytes int64) *Pipeline {
	if log == nil {
		log = slog.Default()
	}
	return &Pipeline{
		log:          log,
		idx:          idx,
		maxFileBytes: maxFileBytes,
		input:        make(chan FileInput, 64),
		stopCh:       make(chan struct{}),
	}
}

// Start kicks off the worker goroutine. Idempotent — subsequent
// calls are no-ops (guarded by a sync.Once). Returns immediately.
func (p *Pipeline) Start() {
	p.startOnce.Do(func() {
		p.wg.Add(1)
		go p.run()
	})
}

// Submit enqueues a file for extraction. If the pipeline's input channel
// is full, the call blocks until a slot opens or Stop is called. In M2.2a
// we assume the Engine's file-completion tracker produces events at a
// rate compatible with our extract throughput; M5 adds a proper rate
// limiter if that assumption breaks.
func (p *Pipeline) Submit(in FileInput) bool {
	select {
	case p.input <- in:
		return true
	case <-p.stopCh:
		return false
	}
}

// Stop signals the worker to finish its current file and exit, then
// waits for it. Idempotent.
func (p *Pipeline) Stop() {
	p.stopOnce.Do(func() {
		close(p.stopCh)
	})
	p.wg.Wait()
}

// run is the worker loop. It reads from the input channel until the
// channel closes or Stop is signalled.
func (p *Pipeline) run() {
	defer p.wg.Done()
	for {
		select {
		case <-p.stopCh:
			return
		case in, ok := <-p.input:
			if !ok {
				return
			}
			p.handle(in)
		}
	}
}

// handle runs dispatch + extraction + indexing for a single input.
// Errors are logged and otherwise swallowed; one bad file must not stop
// the pipeline. Updates the per-infohash counters exactly once per call
// so the progress bar advances whether or not this file had any text.
func (p *Pipeline) handle(in FileInput) {
	counters := p.countersFor(in.InfoHash)
	defer counters.processed.Add(1)

	candidate := extractors.Candidate{
		Path: in.Path,
		Size: in.Size,
	}
	ex, mime := extractors.Dispatch(candidate)
	if ex == nil {
		// Debug-level only: "no extractor" is the common case on
		// mixed-media torrents (mp3/video/etc.) and we don't want
		// to log every file. Tests and operators can crank the
		// logger to debug if they need to see why a specific path
		// was skipped.
		p.log.Debug("pipeline.no_extractor",
			"path", in.Path, "mime", mime, "size", in.Size)
		counters.skipped.Add(1)
		return
	}

	r, err := in.OpenReader()
	if err != nil {
		p.log.Warn("pipeline.open_failed",
			"path", in.Path, "info_hash", in.InfoHash, "err", err)
		counters.failed.Add(1)
		return
	}
	if c, ok := r.(io.Closer); ok {
		defer c.Close()
	}

	chunks, err := safeExtract(ex, r, p.maxFileBytes)
	if err != nil {
		p.log.Debug("pipeline.extract_skip",
			"path", in.Path, "extractor", ex.Name(), "err", err)
		counters.skipped.Add(1)
		return
	}
	if len(chunks) == 0 {
		counters.skipped.Add(1)
		return
	}

	writeErrors := 0
	for ci, chunk := range chunks {
		doc := ContentDoc{
			InfoHash:   strings.ToLower(in.InfoHash),
			FileIndex:  in.FileIndex,
			FilePath:   in.Path,
			FileSize:   in.Size,
			Mime:       mime,
			Text:       chunk.Text,
			Extractor:  ex.Name(),
			IndexedAt:  time.Now().UTC(),
			ChunkIndex: ci,
		}
		if err := p.idx.IndexContent(doc); err != nil {
			p.log.Warn("pipeline.index_failed",
				"path", in.Path,
				"chunk", ci,
				"err", err,
			)
			writeErrors++
			continue
		}
	}

	if writeErrors == len(chunks) {
		counters.failed.Add(1)
	} else {
		counters.extracted.Add(1)
	}

	p.log.Info("pipeline.extracted",
		"info_hash", in.InfoHash,
		"path", in.Path,
		"extractor", ex.Name(),
		"chunks", len(chunks),
		"ext", strings.ToLower(filepath.Ext(in.Path)),
	)
}

// countersFor returns the (lazily-created) counters for one infohash.
// Normalises the key to lowercase so callers can pass either form.
func (p *Pipeline) countersFor(infohash string) *ihCounters {
	key := strings.ToLower(infohash)
	if v, ok := p.counters.Load(key); ok {
		return v.(*ihCounters)
	}
	fresh := &ihCounters{}
	actual, _ := p.counters.LoadOrStore(key, fresh)
	return actual.(*ihCounters)
}

// Stats returns a point-in-time snapshot of the extraction counters
// for one torrent. Safe to call from any goroutine; cheap enough to
// invoke from a polling HTTP handler once per torrent per poll tick.
// Returns a zero-valued struct for an infohash the pipeline has
// never seen.
func (p *Pipeline) Stats(infohash string) PipelineStats {
	key := strings.ToLower(infohash)
	v, ok := p.counters.Load(key)
	if !ok {
		return PipelineStats{}
	}
	c := v.(*ihCounters)
	return PipelineStats{
		Processed: c.processed.Load(),
		Extracted: c.extracted.Load(),
		Skipped:   c.skipped.Load(),
		Failed:    c.failed.Load(),
	}
}

// safeExtract runs ex.Extract with a panic recovery net. Extractors
// handle adversarial input (torrent payloads from the network); a
// single malformed file must not terminate the whole daemon. A
// recovered panic is converted into an error, so the caller sees it
// as an ordinary extract failure and the worker keeps running.
func safeExtract(ex extractors.Extractor, r io.Reader, maxBytes int64) (chunks []extractors.Chunk, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			chunks = nil
			err = fmt.Errorf("pipeline: extractor %q panicked: %v", ex.Name(), rec)
		}
	}()
	return ex.Extract(r, maxBytes)
}
