package indexer

import (
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
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
// the pipeline.
func (p *Pipeline) handle(in FileInput) {
	candidate := extractors.Candidate{
		Path: in.Path,
		Size: in.Size,
	}
	ex, mime := extractors.Dispatch(candidate)
	if ex == nil {
		// No extractor claimed this file. That is the normal case for
		// videos, images, archives, etc. We silently skip them for
		// M2.2a; later milestones may want a metrics counter.
		return
	}

	r, err := in.OpenReader()
	if err != nil {
		p.log.Warn("pipeline.open_failed",
			"path", in.Path, "info_hash", in.InfoHash, "err", err)
		return
	}
	if c, ok := r.(io.Closer); ok {
		defer c.Close()
	}

	chunks, err := ex.Extract(r, p.maxFileBytes)
	if err != nil {
		p.log.Debug("pipeline.extract_skip",
			"path", in.Path, "extractor", ex.Name(), "err", err)
		return
	}
	if len(chunks) == 0 {
		return
	}

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
			continue
		}
	}

	p.log.Info("pipeline.extracted",
		"info_hash", in.InfoHash,
		"path", in.Path,
		"extractor", ex.Name(),
		"chunks", len(chunks),
		"ext", strings.ToLower(filepath.Ext(in.Path)),
	)
}
