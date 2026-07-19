package engine

import (
	"errors"
	"io"
	"time"

	"github.com/swartznet/swartznet/internal/indexer"
)

// indexExtractCap is the global per-file extract ceiling handed to the
// pipeline: without it PDF/EPUB/DOCX buffer unbounded (SPEC §5.5).
const indexExtractCap = 100 * 1024 * 1024

// SetIndex attaches (or, with nil, detaches) the Layer-L index. It stops any
// existing pipeline first, then builds and starts a fresh one for a non-nil
// index. The daemon calls this once after opening the index.
func (e *Engine) SetIndex(idx *indexer.Index) {
	e.idxMu.Lock()
	if e.pipeline != nil {
		e.pipeline.Stop()
		e.pipeline = nil
	}
	e.idx = idx
	if idx != nil {
		e.pipeline = indexer.NewPipeline(idx, e.log, indexExtractCap)
		e.pipeline.Start()
	}
	e.idxMu.Unlock()
	// Wire (or unwire) the Layer-S local searcher so inbound sn_search queries
	// answer from the same index — swarmsearch never imports Bleve.
	if e.swarm != nil {
		if idx != nil {
			e.swarm.SetSearcher(&indexerSearcher{idx: idx})
		} else {
			e.swarm.SetSearcher(nil)
		}
	}
}

func (e *Engine) index() (*indexer.Index, *indexer.Pipeline) {
	e.idxMu.Lock()
	defer e.idxMu.Unlock()
	return e.idx, e.pipeline
}

// SetTorrentIndexing toggles per-torrent indexing. When off, both the
// torrent-doc and content-doc paths skip; the change is prospective —
// already-indexed chunks are not recalled. Idempotent, persisted.
func (e *Engine) SetTorrentIndexing(ihHex string, enabled bool) error {
	h, err := e.handleByHex(ihHex)
	if err != nil {
		return err
	}
	h.setIndexing(enabled)
	e.log.Info("engine.torrent_indexing_set", "info_hash", h.InfoHashHex(), "enabled", enabled)
	e.persistState(h)
	// Enabling indexing AFTER metadata already arrived must write the torrent-
	// level doc that autoIndex skipped while indexing was off. autoIndex runs
	// once per handle and is never re-spawned, and neither the content-file
	// ingest nor the hourly rescan ever writes the torrent-level doc — so without
	// this, a torrent enabled at runtime stays unsearchable by name until a
	// restart. If metadata has NOT arrived yet, the still-waiting autoIndex will
	// write it (isIndexing() is now true), so we only act on the ready case.
	if enabled && !h.companion {
		select {
		case <-h.T.GotInfo():
			e.writeTorrentDoc(h)
		default:
		}
	}
	return nil
}

// autoIndex writes the torrent-level document once metadata arrives. Spawned
// per handle; also fires for restored torrents, which is how a schema
// rebuild repopulates torrent docs.
func (e *Engine) autoIndex(h *Handle) {
	// Companion bookkeeping torrents are never indexed, minted, or Layer-D
	// published — they would otherwise pollute this node's own published corpus
	// and leak "swartznet-content-index-*" filenames onto the DHT keyword index.
	if h.companion {
		return
	}
	// Wait for metadata WITHOUT a wall-clock cap (bounded by engine close +
	// torrent removal): a prior 5-min timeout meant a magnet whose metadata
	// arrived later was never indexed/minted/published, mirroring the
	// autoDownload late-metadata bug.
	select {
	case <-h.T.GotInfo():
	case <-e.bgCtx.Done():
		return
	case <-h.removed:
		return
	}
	// Mint Aggregate records from the torrent name-keywords BEFORE the Layer-L
	// gate: a signing node contributes to reconciliation even with --no-index
	// (gated on the signer + cache, not on per-torrent indexing).
	e.mintAggregateRecords(h)

	// Publish this torrent's NAME-keywords to Layer D (BEP-44). No-op unless a
	// publisher is active (identity present, publishing not suppressed). This
	// sits alongside record-minting, BEFORE the Layer-L gate — per-torrent
	// indexing-off still publishes existence; only --no-index / --no-dht-publish
	// suppress network-visible publication.
	e.publishTorrent(h)

	e.writeTorrentDoc(h)
}

// writeTorrentDoc writes this torrent's Layer-L document when the index is open
// and per-torrent indexing is on. Called by autoIndex once metadata arrives, and
// again by SetTorrentIndexing when indexing is re-enabled at runtime. The
// underlying IndexTorrent is an idempotent upsert, so a redundant call (both
// paths firing on a metadata/enable race) is harmless.
func (e *Engine) writeTorrentDoc(h *Handle) {
	idx, _ := e.index()
	if idx == nil || !h.isIndexing() {
		return
	}
	doc := e.indexerDocFromTorrent(h)
	if err := idx.IndexTorrent(doc); err != nil {
		e.log.Warn("indexer.index_failed", "info_hash", doc.InfoHash, "err", err)
		return
	}
	e.log.Info("indexer.indexed", "info_hash", doc.InfoHash, "name", doc.Name, "files", doc.FileCount, "size", doc.SizeBytes)
	e.trustedPublisherAutoConfirm(h)
}

// trustedPublisherAutoConfirm adds a trust-listed publisher's torrent to the
// known-good Bloom the moment metadata arrives — the deliberate trust
// relationship substitutes for the completion signal, so it need not wait
// for the download. Untrusted torrents reach the Bloom only via completion
// or an explicit confirm. Like completion, this is bloom.Add ONLY (D22).
func (e *Engine) trustedPublisherAutoConfirm(h *Handle) {
	signedBy := h.SignedBy()
	if signedBy == "" {
		return
	}
	store := e.TrustStore()
	if store == nil || !store.IsTrusted(signedBy) {
		return
	}
	bloom := e.KnownGoodBloom()
	if bloom == nil {
		return
	}
	ih := h.T.InfoHash()
	bloom.Add(ih[:])
	e.log.Info("engine.bloom.trusted_publisher_confirmed", "info_hash", h.InfoHashHex(), "pubkey", signedBy, "label", store.Label(signedBy))
	e.Checkpoint()
}

func (e *Engine) indexerDocFromTorrent(h *Handle) indexer.TorrentDoc {
	t := h.T
	var paths []string
	for _, f := range t.Files() {
		paths = append(paths, f.DisplayPath())
	}
	mi := t.Metainfo()
	trackers := mi.UpvertedAnnounceList().DistinctValues()
	if len(trackers) == 0 && mi.Announce != "" {
		trackers = []string{mi.Announce}
	}
	return indexer.TorrentDoc{
		InfoHash:  t.InfoHash().HexString(),
		Name:      t.Name(),
		FilePaths: paths,
		Trackers:  trackers,
		SizeBytes: t.Length(),
		FileCount: len(paths),
		SignedBy:  h.SignedBy(),
	}
}

// ingestFileEvents feeds completed files into the extraction pipeline. It
// binds the subscription ONCE (a fresh subscription per call would hang) and
// wraps each anacrolix reader in the ReadSeekerAt shim so extractors needing
// io.ReaderAt (ZIM) work against the live pipeline — the §6 defect the shim
// closes.
func (e *Engine) ingestFileEvents(h *Handle) {
	events := h.SubscribeFileEvents()
	for {
		select {
		case <-e.bgCtx.Done():
			return
		case <-h.removed:
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			_, pipeline := e.index()
			if pipeline == nil || !h.isIndexing() {
				continue
			}
			fileIndex := ev.FileIndex
			pipeline.Submit(indexer.FileInput{
				InfoHash:  ev.InfoHash,
				FileIndex: fileIndex,
				Path:      ev.Path,
				Size:      ev.Size,
				OpenReader: func() (io.Reader, error) {
					return e.openFileReader(h, fileIndex)
				},
			})
		}
	}
}

// openFileReader returns a ReadSeekerAt-wrapped reader for the file at index.
func (e *Engine) openFileReader(h *Handle, fileIndex int) (io.Reader, error) {
	files := h.T.Files()
	if fileIndex < 0 || fileIndex >= len(files) {
		return nil, errors.New("engine: file index out of range")
	}
	f := files[fileIndex]
	// Bound the reader to the file length: anacrolix's File.NewReader
	// over-reads a big buffer past the file into the next file's bytes.
	return indexer.NewReadSeekerAt(f.NewReader(), f.Length()), nil
}

// runIndexRescan is the hourly recovery goroutine (rebuild-only — the legacy
// dropped file-complete events with no recovery, SPEC §6.3). Each tick
// re-submits completed, not-yet-submitted files of every indexing torrent.
func (e *Engine) runIndexRescan() {
	ticker := time.NewTicker(e.rescanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-e.bgCtx.Done():
			return
		case <-ticker.C:
			e.indexRescanOnce()
		}
	}
}

func (e *Engine) indexRescanOnce() {
	_, pipeline := e.index()
	if pipeline == nil {
		return
	}
	for _, h := range e.Torrents() {
		if !h.isIndexing() || h.T.Info() == nil {
			continue
		}
		for i, f := range h.T.Files() {
			if f.Length() <= 0 || f.BytesCompleted() != f.Length() {
				continue
			}
			if pipeline.WasSubmitted(h.InfoHashHex(), i) {
				continue
			}
			fileIndex := i
			path := f.DisplayPath()
			e.log.Info("engine.index_rescan.resubmit", "info_hash", h.InfoHashHex(), "file_index", fileIndex, "path", path)
			pipeline.Submit(indexer.FileInput{
				InfoHash:  h.InfoHashHex(),
				FileIndex: fileIndex,
				Path:      path,
				Size:      f.Length(),
				OpenReader: func() (io.Reader, error) {
					return e.openFileReader(h, fileIndex)
				},
			})
		}
	}
}

// ForgetIndex deletes a torrent's index documents (torrent + all content),
// keeping the downloaded files. A no-op when no index is attached. This is
// the reachable "Forget" the legacy never wired (§6/F36).
func (e *Engine) ForgetIndex(ihHex string) {
	idx, pipeline := e.index()
	if pipeline != nil {
		pipeline.ForgetSubmitted(ihHex)
	}
	if idx == nil {
		return
	}
	if err := idx.DeleteTorrent(ihHex); err != nil {
		e.log.Warn("indexer.delete_failed", "info_hash", ihHex, "err", err)
	}
	if _, err := idx.DeleteContentForTorrent(ihHex); err != nil {
		e.log.Warn("indexer.delete_content_failed", "info_hash", ihHex, "err", err)
	}
}

// IndexStats returns (processed, extracted) pipeline counters for a torrent,
// feeding the snapshot's IndexedFiles/IndexExtracted.
func (e *Engine) IndexStats(ihHex string) (processed, extracted int) {
	_, pipeline := e.index()
	if pipeline == nil {
		return 0, 0
	}
	s := pipeline.Stats(ihHex)
	return int(s.Processed), int(s.Extracted)
}

// Index returns the attached index (nil when Layer L is off) for daemon
// adapters that search or report stats.
func (e *Engine) Index() *indexer.Index {
	idx, _ := e.index()
	return idx
}
