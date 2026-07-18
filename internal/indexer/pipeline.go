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

// Pipeline consumes completed-file inputs, runs a text extractor, and
// writes the result to the index as ContentDocs. It is deliberately
// decoupled from the engine: the only inputs are FileInput values plus
// the *Index to write into, so the package never learns about
// anacrolix/torrent. The engine produces FileInputs from its
// file-complete event stream (adding the file reader only the live
// torrent knows how to construct).
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
	// lowercased hex infohashes, values are *ihCounters, populated
	// lazily on first Submit for a given infohash.
	counters sync.Map

	// submittedMu guards submitted: every (infohash, fileIndex) ever
	// successfully enqueued. The engine's hourly rescan consults it via
	// WasSubmitted so only dropped file-complete events are resubmitted
	// (without it, every tick would re-extract the whole library); the
	// Forget path clears it via ForgetSubmitted. A stale entry is
	// harmless — deterministic doc IDs make re-indexing idempotent.
	submittedMu sync.Mutex
	submitted   map[string]map[int]struct{}
}

// ihCounters is the set of atomic tallies kept for a single torrent.
// processed advances past every file the pipeline has finished with,
// whether it produced chunks, was skipped by dispatch, or errored.
type ihCounters struct {
	processed atomic.Int64
	extracted atomic.Int64
	skipped   atomic.Int64
	failed    atomic.Int64
}

// PipelineStats is a snapshot of the extraction counters for one torrent.
// Zero-valued when the infohash has never been submitted. Invariant:
// Processed == Extracted + Skipped + Failed.
type PipelineStats struct {
	// Processed is the total count of files the pipeline has finished
	// handling.
	Processed int64
	// Extracted is the count of files that yielded at least one content
	// chunk.
	Extracted int64
	// Skipped is the count of files where no extractor matched,
	// extraction returned no chunks, or extraction errored / timed out /
	// panicked (frozen legacy accounting, SPEC §5.5). Normal for images,
	// videos, archives.
	Skipped int64
	// Failed is the count of files where OpenReader failed or the
	// index-write failed for every chunk.
	Failed int64
}

// FileInput describes a single completed file ready for extraction. The
// reader is provided lazily via OpenReader so the pipeline can decide
// whether to extract at all before touching disk.
type FileInput struct {
	InfoHash  string
	FileIndex int
	Path      string
	Size      int64

	// OpenReader returns a reader for the file contents. The pipeline
	// closes it (when it implements io.Closer) inside the extracting
	// goroutine, strictly after Extract returns.
	OpenReader func() (io.Reader, error)
}

// NewPipeline constructs a Pipeline over idx. maxFileBytes is the
// per-file extract byte cap; zero means "use the extractor's default".
// Start must be called before any input is queued.
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
		submitted:    make(map[string]map[int]struct{}),
	}
}

// Start kicks off the single worker goroutine. Idempotent (sync.Once);
// returns immediately.
func (p *Pipeline) Start() {
	p.startOnce.Do(func() {
		p.wg.Add(1)
		go p.run()
	})
}

// Submit enqueues a file for extraction. When the input channel is full,
// the call BLOCKS until a slot opens or Stop is called; returns false
// after Stop. Drop-never-block behavior lives upstream in the engine's
// file-tracker subscription buffer, not here.
func (p *Pipeline) Submit(in FileInput) bool {
	select {
	case p.input <- in:
		p.markSubmitted(in.InfoHash, in.FileIndex)
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

// run is the worker loop, reading from the input channel until the
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
// the pipeline. The per-infohash counters advance exactly once per call
// whether or not this file had any text.
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
		// mixed-media torrents and logging every file would drown the
		// log. Crank the logger to debug to see why a path skipped.
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
	// r is closed by safeExtract's extracting goroutine (after Extract
	// returns), never here: closing from this goroutine would race a
	// still-running Read when the extract watchdog fires.

	chunks, err := safeExtract(p.log, ex, r, p.maxFileBytes)
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

// countersFor returns the (lazily-created) counters for one infohash,
// normalising the key to lowercase so callers can pass either form.
func (p *Pipeline) countersFor(infohash string) *ihCounters {
	key := strings.ToLower(infohash)
	if v, ok := p.counters.Load(key); ok {
		return v.(*ihCounters)
	}
	fresh := &ihCounters{}
	actual, _ := p.counters.LoadOrStore(key, fresh)
	return actual.(*ihCounters)
}

// Stats returns a point-in-time snapshot of the extraction counters for
// one torrent. Safe from any goroutine; cheap enough for per-poll-tick
// HTTP handlers. Zero-valued for an infohash the pipeline has never seen.
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

// markSubmitted records a successful enqueue in the submitted-set.
func (p *Pipeline) markSubmitted(infohash string, fileIndex int) {
	key := strings.ToLower(infohash)
	p.submittedMu.Lock()
	defer p.submittedMu.Unlock()
	files, ok := p.submitted[key]
	if !ok {
		files = make(map[int]struct{})
		p.submitted[key] = files
	}
	files[fileIndex] = struct{}{}
}

// WasSubmitted reports whether the (infohash, fileIndex) pair was ever
// successfully enqueued on this Pipeline. The engine's hourly rescan uses
// it as the dedup guard: only files that never made it into the queue
// (dropped file-complete events) get resubmitted.
func (p *Pipeline) WasSubmitted(infohash string, fileIndex int) bool {
	key := strings.ToLower(infohash)
	p.submittedMu.Lock()
	defer p.submittedMu.Unlock()
	_, ok := p.submitted[key][fileIndex]
	return ok
}

// ForgetSubmitted drops every submitted-set entry AND the accumulated
// counters for the given infohash, so a later re-add re-indexes cleanly and
// its index stats start from zero (rather than double-counting the prior
// run). Both maps are cleared symmetrically to bound memory for a churning
// client.
func (p *Pipeline) ForgetSubmitted(infohash string) {
	key := strings.ToLower(infohash)
	p.submittedMu.Lock()
	delete(p.submitted, key)
	p.submittedMu.Unlock()
	p.counters.Delete(key)
}

// extractWatchdog is the HARD per-extract deadline. An extract running
// longer is almost certainly wedged on pathological input. When the
// deadline fires, safeExtract abandons the extract and the worker moves
// on; the extractor goroutine is left running (Go cannot kill a
// goroutine), so a truly stuck extractor leaks one goroutine — an
// accepted, bounded cost, far better than pinning the single worker
// forever. First-line protection remains the input size caps and the
// panic recovery below; the deadline guarantees worker progress.
//
// A var (not const) so tests can shrink it; production never mutates it.
var extractWatchdog = 60 * time.Second

// errExtractTimeout is returned when an extract exceeds extractWatchdog.
// The worker treats it like any other extract failure.
var errExtractTimeout = fmt.Errorf("pipeline: extract exceeded %s hard deadline", extractWatchdog)

// safeExtract runs ex.Extract with a panic recovery net and a hard
// deadline. Extractors face adversarial input; one malformed file must
// neither crash nor wedge the daemon. The extract runs in a child
// goroutine selected against a timer; on timeout safeExtract returns
// errExtractTimeout and the wedged goroutine is left to finish or leak.
// A recovered panic is converted into an ordinary extract error.
//
// safeExtract owns closing r (when it implements io.Closer). The Close
// runs in the extracting goroutine's defer, strictly after Extract
// returns — never in the caller — so a timed-out extract can never have
// its reader closed out from under a concurrent Read (anacrolix
// torrent.Reader's Read and Close are not safe to race).
func safeExtract(log *slog.Logger, ex extractors.Extractor, r io.Reader, maxBytes int64) (chunks []extractors.Chunk, err error) {
	if log == nil {
		log = slog.Default()
	}

	type result struct {
		chunks []extractors.Chunk
		err    error
	}
	// Buffered so the child goroutine never blocks on send even after
	// safeExtract has already returned on the timeout path.
	done := make(chan result, 1)

	go func() {
		// Registered before the recover defer (LIFO), so the Close also
		// runs when Extract panics — always after the last Read.
		if c, ok := r.(io.Closer); ok {
			defer c.Close()
		}
		defer func() {
			if rec := recover(); rec != nil {
				done <- result{nil, fmt.Errorf("pipeline: extractor %q panicked: %v", ex.Name(), rec)}
			}
		}()
		c, e := ex.Extract(r, maxBytes)
		done <- result{c, e}
	}()

	timer := time.NewTimer(extractWatchdog)
	defer timer.Stop()

	select {
	case res := <-done:
		return res.chunks, res.err
	case <-timer.C:
		log.Warn("pipeline.extract_timeout",
			"extractor", ex.Name(),
			"deadline", extractWatchdog.String(),
			"max_bytes", maxBytes,
		)
		return nil, errExtractTimeout
	}
}
