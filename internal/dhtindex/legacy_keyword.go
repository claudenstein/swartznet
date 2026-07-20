package dhtindex

import (
	"context"
	"log/slog"
	"time"

	"github.com/swartznet/swartznet/contracts/dhtschema"
)

// legacyKeyword is the v1 RecordBackend: one BEP-44 mutable item per
// (publisher, keyword), read-modify-write against a persistent Manifest. It
// composes a Putter, a Getter, the Manifest (with oldest-hit eviction), the
// per-keyword throttle, and the shared fail-closed checkPutStats (inside
// Putter.Put). config.LayerDMode=="legacy" selects it.
type legacyKeyword struct {
	log      *slog.Logger
	put      Putter
	get      Getter
	manifest *Manifest
	// putTimeout bounds a single Put traversal; minPutInterval is the hard
	// per-keyword publish budget (skip a put issued less than this long ago).
	putTimeout     time.Duration
	minPutInterval time.Duration
}

// NewLegacyKeyword builds the legacy backend. put/get may be shared fakes in
// tests; manifest is persistent (empty path ⇒ in-memory). log may be nil.
func NewLegacyKeyword(put Putter, get Getter, manifest *Manifest, opts PublisherOptions, log *slog.Logger) *legacyKeyword {
	if log == nil {
		log = slog.Default()
	}
	return &legacyKeyword{
		log:            log,
		put:            put,
		get:            get,
		manifest:       manifest,
		putTimeout:     opts.PutTimeout,
		minPutInterval: opts.MinPutInterval,
	}
}

// Publish updates the manifest for each keyword and re-publishes it. One bad
// keyword never stops the rest; the manifest is saved once at the end.
func (b *legacyKeyword) Publish(ctx context.Context, keywords []string, hit dhtschema.KeywordHit) error {
	for _, kw := range keywords {
		if _, err := b.manifest.AddHit(kw, hit); err != nil {
			b.log.Warn("dhtindex.legacy.add_hit_err", "kw", kw, "err", err)
			continue
		}
		b.publishOne(ctx, kw)
	}
	return b.manifest.Save()
}

// Refresh re-publishes every manifest entry. The ctx (tied to the worker's
// stopCh) short-circuits between keywords so Stop is prompt.
func (b *legacyKeyword) Refresh(ctx context.Context) error {
	for keyword := range b.manifest.Snapshot() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		b.publishOne(ctx, keyword)
	}
	return b.manifest.Save()
}

// Retract scrubs an infohash from every keyword and persists.
func (b *legacyKeyword) Retract(_ context.Context, ih [20]byte) error {
	if b.manifest.RemoveAllHits(ih[:]) > 0 {
		return b.manifest.Save()
	}
	return nil
}

// Lookup resolves one token against one indexer's namespace: SaltForKeyword →
// Getter.Get(indexerPub, salt) → hits. Merge/score happen above the port.
func (b *legacyKeyword) Lookup(ctx context.Context, indexerPub [32]byte, token string) ([]dhtschema.KeywordHit, error) {
	salt, err := dhtschema.SaltForKeyword(token)
	if err != nil {
		return nil, err
	}
	v, err := b.get.Get(ctx, indexerPub, salt)
	if err != nil {
		return nil, err
	}
	return v.Hits, nil
}

// Status snapshots the manifest into a PublisherStatus.
func (b *legacyKeyword) Status() PublisherStatus {
	snap := b.manifest.Snapshot()
	st := PublisherStatus{PubKey: b.put.PublicKey(), TotalKeywords: len(snap)}
	for kw, entry := range snap {
		st.TotalHits += len(entry.Hits)
		st.Keywords = append(st.Keywords, PublisherKeywordStatus{
			Keyword:       kw,
			HitsCount:     len(entry.Hits),
			LastPublished: entry.LastPublished,
			PublishCount:  entry.PublishCount,
			LastError:     entry.LastError,
		})
	}
	return st
}

// Close persists the manifest one last time.
func (b *legacyKeyword) Close() error { return b.manifest.Save() }

// publishOne re-publishes the manifest entry for keyword. Errors are recorded
// on the entry and logged, never returned — one bad keyword must not stop the
// worker. The per-keyword throttle skips the put (no network I/O) when the
// keyword was published less than minPutInterval ago; zero disables it (tests).
// This is the self-DoS guard: anacrolix has no put rate cap.
func (b *legacyKeyword) publishOne(parent context.Context, keyword string) {
	snap := b.manifest.Snapshot()
	entry, ok := snap[keyword]
	if !ok {
		return
	}
	if b.minPutInterval > 0 && !entry.LastPublished.IsZero() &&
		time.Since(entry.LastPublished) < b.minPutInterval {
		b.log.Debug("dhtindex.legacy.put_throttled",
			"keyword", keyword,
			"since_last", time.Since(entry.LastPublished).String(),
			"min_interval", b.minPutInterval.String())
		return
	}
	salt, err := dhtschema.SaltForKeyword(keyword)
	if err != nil {
		b.manifest.MarkFailed(keyword, err)
		return
	}
	value := dhtschema.KeywordValue{Hits: entry.Hits}
	timeout := b.putTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if err := b.put.Put(ctx, salt, value); err != nil {
		b.log.Warn("dhtindex.legacy.put_err", "keyword", keyword, "hits", len(entry.Hits), "err", err)
		b.manifest.MarkFailed(keyword, err)
		return
	}
	b.manifest.MarkPublished(keyword, time.Now())
	b.log.Debug("dhtindex.legacy.put_ok", "keyword", keyword, "hits", len(entry.Hits))
}
