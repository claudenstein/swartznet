package dhtindex

import (
	"context"
	"fmt"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/krpc"
)

// SampleInfohashesResult is the parsed form of a BEP-51 sample_infohashes
// response. Samples is the list of 20-byte infohashes the node volunteered
// (zero-length for an old client or an empty window). Interval is the
// politeness hint (seconds) — callers MUST wait ≥Interval before re-querying
// the same node. Num is the node's total tracked infohash count. Nodes is the
// merged IPv4+IPv6 neighbour slice that feeds a crawl frontier.
type SampleInfohashesResult struct {
	Samples  []krpc.ID
	Interval int64
	Num      int64
	Nodes    []krpc.NodeInfo
}

// SampleInfohashes issues ONE BEP-51 sample_infohashes query to addr via
// server. target is the 20-byte node ID selecting which keyspace slice the
// node samples from; crawlers pick random targets across the 2^160 space. This
// is the narrow primitive only — the crawler worker pool (target selection,
// per-node Interval politeness, frontier expansion, metainfo fetch + admission)
// is deferred to Slice 12. Keeping the primitive narrow makes it trivially
// unit-testable against a loopback dht.Server, and it rides the standard BEP-51
// verb — no new verb or port.
func SampleInfohashes(ctx context.Context, server *dht.Server, addr dht.Addr, target krpc.ID) (SampleInfohashesResult, error) {
	if server == nil {
		return SampleInfohashesResult{}, fmt.Errorf("dhtindex: nil dht.Server")
	}
	if addr == nil {
		return SampleInfohashesResult{}, fmt.Errorf("dhtindex: nil dht.Addr")
	}
	res := server.Query(ctx, addr, "sample_infohashes", dht.QueryInput{
		MsgArgs: krpc.MsgArgs{Target: target},
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
	out.Nodes = append(out.Nodes, r.Nodes...)
	out.Nodes = append(out.Nodes, r.Nodes6...)
	return out, nil
}
