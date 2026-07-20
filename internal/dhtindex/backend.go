package dhtindex

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"log/slog"
	"time"

	"github.com/swartznet/swartznet/contracts/dhtschema"
)

// RecordBackend is the swappable Layer-D storage seam. One port at
// publish/lookup granularity: the throttle, oldest-hit eviction, and manifest
// are format-specific state INSIDE the backend; the indexer-set, reputation
// filter, and scoring live ABOVE the port in Lookup. Record bytes stay
// codec-owned in contracts/dhtschema and are never exposed through the port,
// so it does not degrade into a lowest-common-denominator between the legacy
// CAS read-modify-write model and the Slice-12 B-tree/RIBLT model.
//
// legacyKeyword is the v1 default; Slice 12 adds aggregatePPMI/composite impls
// behind this same interface without touching Publisher, Lookup, searchmux, or
// httpapi.
type RecordBackend interface {
	// Publish makes this node's own hit discoverable under each name-keyword.
	// It owns format-specific persistence, oldest-hit eviction, the per-keyword
	// throttle, and the MarkPublished/MarkFailed state. Keywords are the
	// tokenized torrent NAME only — never content tokens.
	Publish(ctx context.Context, keywords []string, hit dhtschema.KeywordHit) error
	// Refresh re-announces everything this backend has published. Driven by
	// the Publisher worker's refresh ticker (BEP-44 items expire after 2h).
	Refresh(ctx context.Context) error
	// Retract scrubs an infohash from everything this backend published, so a
	// removed torrent stops being re-announced on the next refresh.
	Retract(ctx context.Context, ih [20]byte) error
	// Lookup resolves ONE already-chosen token against ONE indexer's
	// namespace, returning that indexer's raw hits. It does NOT merge or score
	// — Lookup does that across every indexer's results.
	Lookup(ctx context.Context, indexerPub [32]byte, token string) ([]dhtschema.KeywordHit, error)
	// Status reports per-keyword publish state for GET /publish and /status.
	Status() PublisherStatus
	// Close flushes any format-specific persistence (e.g. the manifest).
	Close() error
}

// PublisherStatus is a point-in-time view of the backend's publish state,
// surfaced by GET /publish and folded into GET /status.
type PublisherStatus struct {
	PubKey        [32]byte
	TotalKeywords int
	TotalHits     int
	Keywords      []PublisherKeywordStatus
}

// PublisherKeywordStatus is one keyword's row in the publisher status.
type PublisherKeywordStatus struct {
	Keyword       string
	HitsCount     int
	LastPublished time.Time
	PublishCount  int
	LastError     string
}

// NewBackend selects a RecordBackend by mode — the single seam keyed on
// config.LayerDMode. "legacy" (or empty) is the shipping per-keyword BEP-44
// backend; "aggregatePPMI" is the signed SNAGG B-tree backend; "composite"
// dual-writes legacy (primary) + aggregate (secondary) so a migration is pure
// adapter-selection with zero data loss. put may be nil for a read-only
// (lookup) backend; priv is the signer (nil for a read-only aggregate, which is
// then inert).
func NewBackend(mode string, priv ed25519.PrivateKey, put Putter, get Getter, manifest *Manifest, opts PublisherOptions, log *slog.Logger) (RecordBackend, error) {
	if log == nil {
		log = slog.Default()
	}
	selfPub := func() [32]byte {
		if put != nil {
			return put.PublicKey()
		}
		return [32]byte{}
	}
	switch mode {
	case "", "legacy":
		return NewLegacyKeyword(put, get, manifest, opts, log), nil
	case "aggregatePPMI":
		return newAggregatePPMI(priv, selfPub(), log), nil
	case "composite":
		return &composite{
			primary:   NewLegacyKeyword(put, get, manifest, opts, log),
			secondary: newAggregatePPMI(priv, selfPub(), log),
			log:       log,
		}, nil
	default:
		return nil, fmt.Errorf("dhtindex: unsupported LayerDMode %q", mode)
	}
}
