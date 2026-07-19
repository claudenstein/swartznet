package dhtindex

import (
	"bytes"
	"context"
	"encoding/hex"
	"testing"

	"github.com/swartznet/swartznet/contracts/dhtschema"
)

func TestAggregatePPMISelfLookup(t *testing.T) {
	t.Parallel()
	priv, pub := genKey(t)
	agg := newAggregatePPMI(priv, pub, discardLog())
	ih := bytes.Repeat([]byte{0xab}, 20)
	if err := agg.Publish(context.Background(), []string{"ubuntu", "desktop"}, dhtschema.KeywordHit{IH: ih}); err != nil {
		t.Fatal(err)
	}
	hits, err := agg.Lookup(context.Background(), pub, "ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hex.EncodeToString(hits[0].IH) != hex.EncodeToString(ih) {
		t.Fatalf("self-lookup hits = %+v", hits)
	}
	// A different pubkey (cross-publisher) is not wired locally → empty.
	var other [32]byte
	other[0] = 0xff
	if h, _ := agg.Lookup(context.Background(), other, "ubuntu"); len(h) != 0 {
		t.Errorf("cross-publisher lookup returned %d hits, want 0", len(h))
	}
	// Status reflects the record set (2 keywords, 2 records).
	if st := agg.Status(); st.TotalKeywords != 2 || st.TotalHits != 2 {
		t.Errorf("status = %+v", st)
	}
}

func TestAggregatePPMIReadOnlyInert(t *testing.T) {
	t.Parallel()
	var pub [32]byte
	agg := newAggregatePPMI(nil, pub, discardLog()) // nil signer
	if err := agg.Publish(context.Background(), []string{"ubuntu"}, dhtschema.KeywordHit{IH: bytes.Repeat([]byte{1}, 20)}); err != nil {
		t.Fatal(err)
	}
	if st := agg.Status(); st.TotalHits != 0 {
		t.Errorf("read-only aggregate published records: %+v", st)
	}
	if h, _ := agg.Lookup(context.Background(), pub, "ubuntu"); len(h) != 0 {
		t.Errorf("read-only aggregate returned hits: %d", len(h))
	}
}

func TestAggregatePPMIRetract(t *testing.T) {
	t.Parallel()
	priv, pub := genKey(t)
	agg := newAggregatePPMI(priv, pub, discardLog())
	ih := bytes.Repeat([]byte{7}, 20)
	var ihArr [20]byte
	copy(ihArr[:], ih)
	agg.Publish(context.Background(), []string{"ubuntu"}, dhtschema.KeywordHit{IH: ih})
	if h, _ := agg.Lookup(context.Background(), pub, "ubuntu"); len(h) != 1 {
		t.Fatal("expected 1 hit before retract")
	}
	if err := agg.Retract(context.Background(), ihArr); err != nil {
		t.Fatal(err)
	}
	if h, _ := agg.Lookup(context.Background(), pub, "ubuntu"); len(h) != 0 {
		t.Errorf("retract left %d hits", len(h))
	}
}

// TestCompositeDualWriteLegacyReadable is the seam DoD: composite dual-writes
// to legacy (primary) + aggregate (secondary), and a LEGACY-ONLY reader still
// returns the hit — proving a mode switch is adapter-selection with no data loss.
func TestCompositeDualWriteLegacyReadable(t *testing.T) {
	t.Parallel()
	priv, pub := genKey(t)
	shared := NewSharedMemoryStore()
	legacy := NewLegacyKeyword(shared.PutterFor(priv), shared.Getter(), mustMem(), PublisherOptions{}, discardLog())
	agg := newAggregatePPMI(priv, pub, discardLog())
	comp := &composite{primary: legacy, secondary: agg, log: discardLog()}

	ih := bytes.Repeat([]byte{0x5a}, 20)
	if err := comp.Publish(context.Background(), []string{"ubuntu"}, dhtschema.KeywordHit{IH: ih, N: "Ubuntu 24.04", S: 9}); err != nil {
		t.Fatal(err)
	}

	// (1) A LEGACY-ONLY backend over the same store still finds the hit.
	legacyOnly := NewLegacyKeyword(nil, shared.Getter(), mustMem(), PublisherOptions{}, discardLog())
	lh, err := legacyOnly.Lookup(context.Background(), pub, "ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	if len(lh) != 1 || lh[0].N != "Ubuntu 24.04" {
		t.Fatalf("legacy-only read after composite write = %+v (dual-write lost the legacy record)", lh)
	}
	// (2) The aggregate secondary also has the record (self-lookup).
	ah, _ := agg.Lookup(context.Background(), pub, "ubuntu")
	if len(ah) != 1 {
		t.Errorf("aggregate secondary missing the record: %d", len(ah))
	}
	// (3) The composite merge returns exactly one deduped hit (same infohash).
	mh, err := comp.Lookup(context.Background(), pub, "ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	if len(mh) != 1 {
		t.Errorf("composite merge = %d hits, want 1 (deduped)", len(mh))
	}
}

// TestCompositeSecondaryErrorTolerated: the secondary's failures never break
// the primary path.
func TestCompositeSecondaryErrorTolerated(t *testing.T) {
	t.Parallel()
	priv, pub := genKey(t)
	shared := NewSharedMemoryStore()
	legacy := NewLegacyKeyword(shared.PutterFor(priv), shared.Getter(), mustMem(), PublisherOptions{}, discardLog())
	comp := &composite{primary: legacy, secondary: &errBackend{}, log: discardLog()}
	if err := comp.Publish(context.Background(), []string{"ubuntu"}, dhtschema.KeywordHit{IH: bytes.Repeat([]byte{1}, 20)}); err != nil {
		t.Fatalf("primary publish should succeed despite secondary error: %v", err)
	}
	if _, err := comp.Lookup(context.Background(), pub, "ubuntu"); err != nil {
		t.Fatalf("primary lookup should succeed despite secondary error: %v", err)
	}
}

// errBackend fails every operation (models a broken secondary).
type errBackend struct{}

func (errBackend) Publish(context.Context, []string, dhtschema.KeywordHit) error { return errFail }
func (errBackend) Refresh(context.Context) error                                 { return errFail }
func (errBackend) Retract(context.Context, [20]byte) error                       { return errFail }
func (errBackend) Lookup(context.Context, [32]byte, string) ([]dhtschema.KeywordHit, error) {
	return nil, errFail
}
func (errBackend) Status() PublisherStatus { return PublisherStatus{} }
func (errBackend) Close() error            { return errFail }

var errFail = context.DeadlineExceeded
