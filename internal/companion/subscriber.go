package companion

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/swartznet/swartznet/internal/indexer"
)

// SubscriberOptions tunes the subscriber.
type SubscriberOptions struct {
	FetchTimeout   time.Duration // bounds one companion download; default 5m
	PointerTimeout time.Duration // bounds one BEP-44 get traversal; default 30s
	Interval       time.Duration // worker re-sync period; default 1h
}

// DefaultSubscriberOptions returns the production defaults.
func DefaultSubscriberOptions() SubscriberOptions {
	return SubscriberOptions{
		FetchTimeout:   5 * time.Minute,
		PointerTimeout: 30 * time.Second,
		Interval:       1 * time.Hour,
	}
}

// SyncResult is the outcome of one Sync.
type SyncResult struct {
	Publisher        string   // 64-hex followed pubkey
	PointerInfoHash  [20]byte // zero on pointer failure
	TorrentsImported int
	ContentImported  int
	GeneratedAt      int64 // publisher-side snapshot ts; 0 unless decode succeeded
	Deduped          bool  // true when an unchanged snapshot was skipped
	Err              error
}

// Subscriber resolves a followed publisher's companion pointer, fetches the
// .torrent FAIL-CLOSED, VERIFIES the snapshot was authored by the followed
// publisher, dedups by GeneratedAt, and imports records stamped with the
// followed pubkey. It never imports internal/dhtindex or internal/engine.
type Subscriber struct {
	getter   PointerGetter
	fetcher  CompanionFetcher
	ingester Ingester
	opts     SubscriberOptions
	log      *slog.Logger

	mu       sync.Mutex
	imported map[[32]byte]int64    // pubkey → last successfully-imported GeneratedAt
	lastIH   map[[32]byte][20]byte // pubkey → last successfully-imported companion infohash
	// epoch counts Forget calls per pubkey. A Sync captures the epoch when it
	// starts and only commits its dedup state if the epoch is unchanged at the
	// end — so an Unfollow (Forget) that races an in-flight Sync is never
	// resurrected by that Sync's late write. Never deleted (keeping it monotonic
	// avoids a re-follow colliding with a still-in-flight older Sync's snapshot).
	epoch map[[32]byte]uint64
}

// NewSubscriber validates its ports and defaults its options.
func NewSubscriber(getter PointerGetter, fetcher CompanionFetcher, ingester Ingester, opts SubscriberOptions, log *slog.Logger) (*Subscriber, error) {
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
		imported: make(map[[32]byte]int64),
		lastIH:   make(map[[32]byte][20]byte),
		epoch:    make(map[[32]byte]uint64),
	}, nil
}

// Sync runs the full pipeline for one followed publisher. res is populated even
// on failure (callers never nil-check).
func (s *Subscriber) Sync(ctx context.Context, pubkey [32]byte) SyncResult {
	pubHex := hex.EncodeToString(pubkey[:])
	res := SyncResult{Publisher: pubHex}

	// Snapshot the Forget epoch at the start. Any dedup-state commit below is
	// suppressed if this changes mid-Sync (an Unfollow raced us), so an in-flight
	// Sync never resurrects state for a publisher we no longer follow.
	s.mu.Lock()
	startEpoch := s.epoch[pubkey]
	s.mu.Unlock()

	getCtx, cancel := context.WithTimeout(ctx, s.opts.PointerTimeout)
	ih, err := s.getter.GetInfohashPointer(getCtx, pubkey, []byte(SaltContentIndex))
	cancel()
	if err != nil {
		res.Err = fmt.Errorf("get pointer: %w", err)
		return res
	}
	res.PointerInfoHash = ih

	// Dedup on the pointer infohash BEFORE fetching: an unchanged pointer
	// (publisher offline, or a re-sync within the interval) needs no re-fetch.
	// The companion payload's GeneratedAt changes the infohash whenever content
	// changes, so a changed pointer means new content worth fetching.
	s.mu.Lock()
	prevIH, seenIH := s.lastIH[pubkey]
	s.mu.Unlock()
	if seenIH && prevIH == ih {
		res.Deduped = true
		s.log.Debug("companion.subscriber.deduped_pointer", "publisher", pubHex)
		return res
	}

	fetchCtx, cancel := context.WithTimeout(ctx, s.opts.FetchTimeout)
	path, err := s.fetcher.FetchCompanionTorrent(fetchCtx, ih)
	cancel()
	if err != nil {
		res.Err = fmt.Errorf("fetch companion torrent: %w", err)
		return res
	}

	idx, err := s.decodeFile(path)
	if err != nil {
		res.Err = fmt.Errorf("decode %s: %w", path, err)
		return res
	}
	res.GeneratedAt = idx.GeneratedAt

	// FIX (§6): the snapshot MUST be authored by the publisher we follow. A
	// pointer/infohash swap or a snapshot signed by a different key is rejected
	// BEFORE any record touches the local index.
	if strings.ToLower(idx.Publisher) != pubHex {
		res.Err = fmt.Errorf("publisher mismatch: snapshot authored by %q, following %q", idx.Publisher, pubHex)
		return res
	}

	// The pointer-infohash dedup above already skips an UNCHANGED snapshot, so a
	// changed infohash here means genuinely new content. Only guard against a
	// ROLLBACK/replay — a new pointer to an OLDER snapshot (GeneratedAt regressed)
	// — and, crucially, still advance lastIH on that reject so we don't re-fetch
	// the same infohash every interval. (The old code used `prev == GeneratedAt`,
	// which dropped new content whenever the publisher reused a timestamp and
	// NEVER advanced lastIH — a permanent freeze + unbounded per-interval refetch.)
	s.mu.Lock()
	prev, ok := s.imported[pubkey]
	s.mu.Unlock()
	if ok && idx.GeneratedAt != 0 && idx.GeneratedAt < prev {
		res.Deduped = true
		s.commitDedup(pubkey, startEpoch, 0, ih, false)
		s.log.Debug("companion.subscriber.rollback_ignored", "publisher", pubHex,
			"generated_at", idx.GeneratedAt, "last_imported", prev)
		return res
	}

	tCount, cCount, err := s.ingest(pubHex, idx)
	res.TorrentsImported = tCount
	res.ContentImported = cCount
	if err != nil {
		res.Err = fmt.Errorf("ingest: %w", err)
		return res
	}
	s.commitDedup(pubkey, startEpoch, idx.GeneratedAt, ih, true)
	s.log.Info("companion.subscriber.synced", "publisher", pubHex,
		"infohash", fmt.Sprintf("%x", ih), "torrents_imported", tCount, "content_imported", cCount)
	return res
}

// Forget drops a publisher's dedup state (last imported GeneratedAt + last
// pointer infohash) so an Unfollow does not leak these maps for the daemon's
// whole lifetime. A later refollow starts fresh.
func (s *Subscriber) Forget(pubkey [32]byte) {
	s.mu.Lock()
	delete(s.imported, pubkey)
	delete(s.lastIH, pubkey)
	s.epoch[pubkey]++ // invalidate any in-flight Sync's pending dedup commit
	s.mu.Unlock()
}

// commitDedup records dedup state for pubkey after a successful import (or a
// rollback-reject, which advances only lastIH). It NO-OPs when a Forget bumped
// the epoch since the Sync began — an Unfollow that raced this Sync must win, so
// the maps are not resurrected for a publisher we no longer follow. Returns
// false when the commit was suppressed.
func (s *Subscriber) commitDedup(pubkey [32]byte, startEpoch uint64, generatedAt int64, ih [20]byte, setImported bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.epoch[pubkey] != startEpoch {
		return false
	}
	if setImported {
		s.imported[pubkey] = generatedAt
	}
	s.lastIH[pubkey] = ih
	return true
}

func (s *Subscriber) decodeFile(path string) (CompanionIndex, error) {
	f, err := os.Open(path)
	if err != nil {
		return CompanionIndex{}, err
	}
	defer f.Close()
	return Decode(f)
}

// IngestReader is a test/in-process shortcut: decode then ingest, stamping
// SignedBy with pubHex. It performs NO publisher verification or dedup (those
// live in Sync) — callers pass the pubkey hex they intend to stamp.
func (s *Subscriber) IngestReader(pubHex string, r interface{ Read([]byte) (int, error) }) (CompanionIndex, int, int, error) {
	idx, err := Decode(r)
	if err != nil {
		return idx, 0, 0, fmt.Errorf("decode: %w", err)
	}
	t, c, err := s.ingest(pubHex, idx)
	return idx, t, c, err
}

// ingest imports the snapshot into the local index, STAMPING every torrent doc
// with SignedBy = the followed pubkey (§6 fix). The first hard write error
// aborts and is returned with partial counts; validation-skips (bad infohash,
// empty text) do not abort.
func (s *Subscriber) ingest(pubHex string, idx CompanionIndex) (torrents, contents int, err error) {
	for _, tr := range idx.Torrents {
		ih := strings.ToLower(tr.InfoHash)
		if len(ih) != 40 {
			s.log.Debug("companion.subscriber.skip_torrent", "reason", "bad infohash", "infohash", ih)
			continue
		}
		var paths []string
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
			SignedBy:  pubHex,
			// The snapshot proves the publisher authored the LIST, not that it
			// signed this torrent — never hijack an existing different attribution.
			PreserveExistingSigner: true,
		}
		if tr.AddedAt > 0 {
			td.AddedAt = time.Unix(tr.AddedAt, 0).UTC()
		}
		if err := s.ingester.IndexTorrent(td); err != nil {
			if errors.Is(err, indexer.ErrForeignTorrent) {
				// The node already attributes this torrent to a different
				// publisher — the snapshot proves the publisher authored the LIST,
				// not that it owns this torrent. Skip it entirely (metadata AND
				// content); do not count it and do not fail the whole sync.
				s.log.Debug("companion.subscriber.skip_foreign_torrent", "publisher", pubHex, "infohash", ih)
				continue
			}
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
					// Never overwrite the node's own locally-extracted content.
					PreserveExisting: true,
				}
				if err := s.ingester.IndexContent(cd); err != nil {
					return torrents, contents, fmt.Errorf("index content %s/%d/%d: %w", ih, fr.Index, ci, err)
				}
				contents++
			}
		}
	}
	return torrents, contents, nil
}
