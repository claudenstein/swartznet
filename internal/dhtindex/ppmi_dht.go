package dhtindex

import (
	"context"
	"fmt"

	"github.com/anacrolix/dht/v2/bep44"
	"github.com/anacrolix/dht/v2/exts/getput"
	"github.com/anacrolix/torrent/bencode"

	"github.com/swartznet/swartznet/contracts/dhtschema"
)

// PPMI (Publisher-Pointer Mutable Item) BEP-44 primitive: a publisher advertises
// its merged SNAGG Aggregate index at the well-known target
// SHA1(pubkey || PPMISalt), where PPMISalt = SHA256("snet.index"). Every
// publisher shares the salt (so a passive DHT observer cannot fingerprint
// SwartzNet by salt); discrimination is at the pubkey. This is the addressing
// primitive the live aggregatePPMI distribution consumes — analogous to the
// BEP-46 companion pointer, sharing the same fail-closed checkPutStats guard.

// PutPPMI publishes v at SHA1(pubkey || PPMISalt), signed per BEP-44. It fails
// closed on a zero-node traversal.
func (a *AnacrolixPutter) PutPPMI(ctx context.Context, v dhtschema.PPMIValue) error {
	encoded, err := dhtschema.EncodePPMI(v)
	if err != nil {
		return err
	}
	var decoded interface{}
	if err := bencode.Unmarshal(encoded, &decoded); err != nil {
		return fmt.Errorf("dhtindex: re-decode PPMI: %w", err)
	}
	target := bep44.MakeMutableTarget(a.public, dhtschema.PPMISalt)
	pubArr := a.public
	seqToPut := func(seq int64) bep44.Put {
		put := bep44.Put{V: decoded, K: &pubArr, Salt: dhtschema.PPMISalt, Seq: nextSeq(seq)}
		put.Sign(a.private)
		return put
	}
	stats, err := getput.Put(ctx, target, a.server, dhtschema.PPMISalt, seqToPut)
	if err != nil {
		return fmt.Errorf("dhtindex: put PPMI: %w", err)
	}
	return checkPutStats(stats, "PPMI put")
}

// GetPPMI resolves a publisher's PPMI item. The signature is verified inside the
// anacrolix get path; DecodePPMI re-applies the ≤1000 pre-unmarshal cap and
// every field-width check against a non-conforming node.
func (a *AnacrolixGetter) GetPPMI(ctx context.Context, pubkey [32]byte) (dhtschema.PPMIValue, error) {
	target := bep44.MakeMutableTarget(pubkey, dhtschema.PPMISalt)
	res, _, err := getput.Get(ctx, target, a.server, nil, dhtschema.PPMISalt)
	if err != nil {
		return dhtschema.PPMIValue{}, fmt.Errorf("dhtindex: get PPMI %x: %w", target, err)
	}
	return dhtschema.DecodePPMI([]byte(res.V))
}
