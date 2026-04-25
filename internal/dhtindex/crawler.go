package dhtindex

import (
	"context"
	"fmt"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/krpc"
)

// SampleInfohashesResult is the parsed form of a BEP-51
// `sample_infohashes` response. Samples is the list of 20-byte
// infohashes the queried node volunteered; it is zero-length
// when the node runs an older client that ignores the query or
// returns an empty window. Interval is the politeness hint the
// node advertised (seconds); callers MUST wait at least Interval
// before re-querying the same node. Num is the queried node's
// total tracked infohash count, useful for prioritising wide
// samplers. Nodes is the closest-known routing-table slice the
// node returned — feeding these back into the crawl frontier is
// how the walk expands.
type SampleInfohashesResult struct {
	Samples  []krpc.ID
	Interval int64
	Num      int64
	Nodes    []krpc.NodeInfo
}

// SampleInfohashes issues one BEP-51 `sample_infohashes` query
// to addr via server. target is the 20-byte node ID that
// determines which slice of the keyspace the queried node
// samples from — crawlers typically pick random targets across
// the full 2^160 space.
//
// This is the low-level primitive. A production crawler layers
// on top: a worker pool that picks targets, respects per-node
// Interval hints, expands to Nodes returned in each reply, and
// for each Samples hash fetches the metainfo and looks for a
// `snet.pubkey` field before calling Bootstrap.CandidateFromCrawl.
// That logic lives outside this package — keeping the primitive
// narrow means it's trivially unit-testable against a loopback
// dht.Server.
func SampleInfohashes(ctx context.Context, server *dht.Server, addr dht.Addr, target krpc.ID) (SampleInfohashesResult, error) {
	if server == nil {
		return SampleInfohashesResult{}, fmt.Errorf("dhtindex: nil dht.Server")
	}
	if addr == nil {
		return SampleInfohashesResult{}, fmt.Errorf("dhtindex: nil dht.Addr")
	}
	res := server.Query(ctx, addr, "sample_infohashes", dht.QueryInput{
		MsgArgs: krpc.MsgArgs{
			Target: target,
		},
	})
	if err := res.ToError(); err != nil {
		return SampleInfohashesResult{}, err
	}
	r := res.Reply.R
	if r == nil {
		return SampleInfohashesResult{}, fmt.Errorf("dhtindex: sample_infohashes reply has no r dict")
	}
	out := SampleInfohashesResult{}
	if r.Samples != nil {
		out.Samples = make([]krpc.ID, 0, len(*r.Samples))
		for _, raw := range *r.Samples {
			out.Samples = append(out.Samples, krpc.ID(raw))
		}
	}
	if r.Interval != nil {
		out.Interval = *r.Interval
	}
	if r.Num != nil {
		out.Num = *r.Num
	}
	// Merge IPv4 + IPv6 neighbour lists — callers usually don't
	// care which one a node advertised.
	for _, n := range r.Nodes {
		out.Nodes = append(out.Nodes, n)
	}
	for _, n := range r.Nodes6 {
		out.Nodes = append(out.Nodes, n)
	}
	return out, nil
}
