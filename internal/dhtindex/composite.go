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
	primary, err := c.primary.Lookup(ctx, indexerPub, token)
	if err != nil {
		return nil, err
	}
	secondary, e2 := c.secondary.Lookup(ctx, indexerPub, token)
	if e2 != nil {
		c.log.Debug("dhtindex.composite.secondary_lookup_err", "err", e2)
		return primary, nil
	}
	seen := make(map[string]struct{}, len(primary))
	out := make([]dhtschema.KeywordHit, 0, len(primary)+len(secondary))
	for _, h := range primary {
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

func (c *composite) Close() error {
	err := c.primary.Close()
	if e2 := c.secondary.Close(); e2 != nil && err == nil {
		err = e2
	}
	return err
}
