// PPMI DHT put/get — the Aggregate-layer publisher pointer
// primitive (SPEC.md §3.1, PROPOSAL.md §2.1).
//
// A PPMI lives at target = SHA1(publisher_pubkey || PPMISalt) and
// carries a small bencoded value pointing at the publisher's
// current companion index torrent. This file adds:
//
//   - PPMIPutter / PPMIGetter interfaces so callers can mock the
//     DHT without a real server.
//   - AnacrolixPutter.PutPPMI and AnacrolixGetter.GetPPMI
//     production implementations.
//   - MemoryPutterGetter.PutPPMI / GetPPMI for in-process tests.
//
// The existing Putter / Getter interfaces (for legacy per-keyword
// KeywordValue items) are unchanged — this adds a parallel track
// so the PPMI path can ship without touching any existing mock.

package dhtindex

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"time"

	"github.com/anacrolix/dht/v2/bep44"
	"github.com/anacrolix/dht/v2/exts/getput"
	"github.com/anacrolix/torrent/bencode"
)

// PPMIPutter publishes a PPMIValue to the DHT under the
// publisher's namespace. Implementations MUST sign the put with
// the publisher's private key per BEP-44.
type PPMIPutter interface {
	PutPPMI(ctx context.Context, value PPMIValue) error
}

// PPMIGetter fetches a PPMIValue from the DHT for a given
// publisher pubkey. Implementations MUST verify the BEP-44
// signature against pubkey before returning.
type PPMIGetter interface {
	GetPPMI(ctx context.Context, pubkey [32]byte) (PPMIValue, error)
}

// PutPPMI publishes the given PPMIValue under the caller's
// publisher pubkey at salt = PPMISalt. The anacrolix DHT library
// handles the sequence-number coordination (gets current seq,
// bumps by 1) so callers don't maintain their own seq state.
func (a *AnacrolixPutter) PutPPMI(ctx context.Context, value PPMIValue) error {
	if value.Ts == 0 {
		value.Ts = time.Now().Unix()
	}
	encoded, err := EncodePPMI(value)
	if err != nil {
		return err
	}
	// Pre-decode to interface{} so bep44.Put.Sign works against a
	// round-trip-identical value.
	var v interface{}
	if err := bencode.Unmarshal(encoded, &v); err != nil {
		return fmt.Errorf("dhtindex: re-decode PPMI: %w", err)
	}

	target := bep44.MakeMutableTarget(a.public, PPMISalt)
	pubArr := a.public
	seqToPut := func(seq int64) bep44.Put {
		put := bep44.Put{
			V:    v,
			K:    &pubArr,
			Salt: PPMISalt,
			Seq:  seq + 1,
		}
		put.Sign(a.private)
		return put
	}
	if _, err := getput.Put(ctx, target, a.server, PPMISalt, seqToPut); err != nil {
		return fmt.Errorf("dhtindex: put PPMI: %w", err)
	}
	return nil
}

// GetPPMI fetches the PPMI under the given publisher pubkey.
// Returns a descriptive error if no item is found or if the
// bencoded value fails schema validation.
func (a *AnacrolixGetter) GetPPMI(ctx context.Context, pubkey [32]byte) (PPMIValue, error) {
	target := bep44.MakeMutableTarget(pubkey, PPMISalt)
	res, _, err := getput.Get(ctx, target, a.server, nil, PPMISalt)
	if err != nil {
		return PPMIValue{}, fmt.Errorf("dhtindex: get PPMI %x: %w", target, err)
	}
	return DecodePPMI([]byte(res.V))
}

// PutPPMI stores a PPMIValue in the memory store.
// Shares the same store backing the legacy Put path; items for
// legacy keyword salts and PPMI salts coexist without collision
// because their targets are distinct (different salts).
func (m *MemoryPutterGetter) PutPPMI(ctx context.Context, value PPMIValue) error {
	encoded, err := EncodePPMI(value)
	if err != nil {
		return err
	}
	var v interface{}
	if err := bencode.Unmarshal(encoded, &v); err != nil {
		return err
	}
	_ = v

	target := sha1.Sum(append(m.pub[:], PPMISalt...))
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ppmiStore[target] = ppmiStoredItem{
		value:  value,
		stored: time.Now(),
	}
	return nil
}

// GetPPMI fetches a PPMIValue for the given publisher pubkey.
func (m *MemoryPutterGetter) GetPPMI(ctx context.Context, pubkey [32]byte) (PPMIValue, error) {
	target := sha1.Sum(append(pubkey[:], PPMISalt...))
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.ppmiStore[target]
	if !ok {
		return PPMIValue{}, errors.New("dhtindex: no PPMI stored for pubkey")
	}
	return item.value, nil
}

// ppmiStoredItem is the in-memory record for a stored PPMI.
type ppmiStoredItem struct {
	value  PPMIValue
	stored time.Time
}
