package dhtindex

import (
	"context"
	"errors"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/krpc"
)

// MetainfoFetcher returns the raw bencoded metainfo bytes for an
// infohash. Production wires this to anacrolix/torrent's BEP-9
// ut_metadata fetch; unit tests stub it with an in-memory map.
// Returning a non-nil error signals the crawler to count the
// fetch as FetchErrors and move on — it MUST NOT abort the whole
// tick.
type MetainfoFetcher func(ctx context.Context, ih krpc.ID) ([]byte, error)

// PublisherSink is where discovered publisher pubkeys go. In
// production this forwards into daemon.Bootstrap.CandidateFromCrawl;
// in tests it's a slice append. The sink receives exactly one
// call per signed sample — unsigned samples and fetch errors are
// silently skipped.
type PublisherSink func(pubkey [32]byte, sigValid bool)

// CrawlOutcome is the tick summary. SampleInfohashes fields let
// the caller schedule the next hop (respect Interval, expand
// frontier via Nodes); the classification counters are useful
// for operational dashboards.
type CrawlOutcome struct {
	Samples   []krpc.ID
	Interval  int64
	Num       int64
	Nodes     []krpc.NodeInfo
	Forwarded int // how many sigValid=true pubkeys reached the sink
	BadSigs   int // signed but fails verification — still forwarded
	Unsigned  int // no snet.pubkey field
	Malformed int // bencode decode failed
	FetchErrs int // fetcher returned non-nil error
}

// CrawlOnce runs one tick of the Channel-B crawler:
//
//  1. Issues a BEP-51 sample_infohashes query to addr.
//  2. For each sample, calls fetch(ctx, sample).
//  3. Classifies the returned metainfo via PublisherFromMetainfo.
//  4. Forwards both sigValid=true and sigValid=false publishers to
//     sink (Bootstrap decides admission; the crawler doesn't).
//
// Returns the full outcome for the caller to act on. A non-nil
// error means the sample query itself failed — the result's
// Samples slice will be empty in that case but the counters
// reflect no fetches were attempted.
//
// One slow fetch does not block the next: callers concerned
// about latency should wrap fetch with a per-call timeout via
// the passed ctx.
func CrawlOnce(
	ctx context.Context,
	server *dht.Server,
	addr dht.Addr,
	target krpc.ID,
	fetch MetainfoFetcher,
	sink PublisherSink,
) (CrawlOutcome, error) {
	if fetch == nil {
		return CrawlOutcome{}, errors.New("dhtindex: nil MetainfoFetcher")
	}
	if sink == nil {
		return CrawlOutcome{}, errors.New("dhtindex: nil PublisherSink")
	}

	sr, err := SampleInfohashes(ctx, server, addr, target)
	if err != nil {
		return CrawlOutcome{}, err
	}
	out := CrawlOutcome{
		Samples:  sr.Samples,
		Interval: sr.Interval,
		Num:      sr.Num,
		Nodes:    sr.Nodes,
	}

	for _, ih := range sr.Samples {
		// Honour context cancellation between samples so a long
		// fetcher loop is responsive to shutdown.
		if err := ctx.Err(); err != nil {
			return out, err
		}
		raw, ferr := fetch(ctx, ih)
		if ferr != nil {
			out.FetchErrs++
			continue
		}
		pk, sigValid, perr := PublisherFromMetainfo(raw)
		switch {
		case perr != nil:
			out.Malformed++
		case sigValid:
			out.Forwarded++
			sink(pk, true)
		case pk != ([32]byte{}):
			// Signed but bad signature. Still forward so
			// reputation / admission policy can see it.
			out.BadSigs++
			sink(pk, false)
		default:
			out.Unsigned++
		}
	}
	return out, nil
}
