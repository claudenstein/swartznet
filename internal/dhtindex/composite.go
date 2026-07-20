package dhtindex

import (
	"context"
	"log/slog"

	"github.com/swartznet/swartznet/contracts/dhtschema"
)

// composite is the migration RecordBackend: it DUAL-WRITES to a primary and a
// secondary backend and DUAL-READS (primary-then-secondary, merged). It lets
// LayerDMode switch from legacy to Aggregate be pure adapter-selection with
// zero application change and zero data loss — the legacy (primary) backend is
// always written and read, so a legacy-only reader still finds every hit while
// the Aggregate (secondary) backend fills in behind it.
//
// The primary's error is authoritative (it is the shipping path); the
// secondary's errors are logged, never fatal, so an experimental Aggregate
// backend can never break publishing or lookup.
type composite struct {
	primary   RecordBackend
	secondary RecordBackend
	log       *slog.Logger
}

func (c *composite) Publish(ctx context.Context, keywords []string, hit dhtschema.KeywordHit) error {
	err := c.primary.Publish(ctx, keywords, hit)
	if e2 := c.secondary.Publish(ctx, keywords, hit); e2 != nil {
		c.log.Debug("dhtindex.composite.secondary_publish_err", "err", e2)
	}
	return err
}

func (c *composite) Refresh(ctx context.Context) error {
	err := c.primary.Refresh(ctx)
	if e2 := c.secondary.Refresh(ctx); e2 != nil {
		c.log.Debug("dhtindex.composite.secondary_refresh_err", "err", e2)
	}
	return err
}

func (c *composite) Retract(ctx context.Context, ih [20]byte) error {
	err := c.primary.Retract(ctx, ih)
	if e2 := c.secondary.Retract(ctx, ih); e2 != nil {
		c.log.Debug("dhtindex.composite.secondary_retract_err", "err", e2)
	}
	return err
}

// Lookup merges both backends' hits, deduplicating by infohash (primary wins on
// metadata). A secondary error is tolerated — the primary result stands.
func (c *composite) Lookup(ctx context.Context, indexerPub [32]byte, token string) ([]dhtschema.KeywordHit, error) {
	// Query BOTH backends and merge. A PRIMARY error must NOT drop the secondary's
	// hits: a routine keyword miss surfaces as an error from every Getter ("not
	// found"), and for an indexer running LayerDMode=aggregatePPMI there is no
	// legacy BEP-44 item at all — the aggregate secondary is the only source.
	// Only fail the lookup when BOTH backends error (a genuine dual failure).
	primary, perr := c.primary.Lookup(ctx, indexerPub, token)
	secondary, serr := c.secondary.Lookup(ctx, indexerPub, token)
	if perr != nil {
		c.log.Debug("dhtindex.composite.primary_lookup_err", "err", perr)
	}
	if serr != nil {
		c.log.Debug("dhtindex.composite.secondary_lookup_err", "err", serr)
	}
	if perr != nil && serr != nil {
		return nil, perr
	}
	// Merge, primary (legacy) winning ties, deduped by infohash.
	seen := make(map[string]struct{}, len(primary)+len(secondary))
	out := make([]dhtschema.KeywordHit, 0, len(primary)+len(secondary))
	for _, h := range primary {
		if _, dup := seen[string(h.IH)]; dup {
			continue
		}
		seen[string(h.IH)] = struct{}{}
		out = append(out, h)
	}
	for _, h := range secondary {
		if _, dup := seen[string(h.IH)]; dup {
			continue
		}
		seen[string(h.IH)] = struct{}{}
		out = append(out, h)
	}
	return out, nil
}

// Status reports the primary (shipping-path) state.
func (c *composite) Status() PublisherStatus { return c.primary.Status() }

// SetDistribution forwards to the aggregate secondary if it is distributable,
// so a composite (legacy primary + aggregate secondary) also seeds its SNAGG
// tree and resolves cross-publisher trees. The legacy primary is unaffected.
func (c *composite) SetDistribution(pub TreePublisher, res TreeResolver) {
	if d, ok := c.secondary.(DistributableBackend); ok {
		d.SetDistribution(pub, res)
	}
}

func (c *composite) Close() error {
	err := c.primary.Close()
	if e2 := c.secondary.Close(); e2 != nil && err == nil {
		err = e2
	}
	return err
}
