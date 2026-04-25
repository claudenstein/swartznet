package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// mockPPMIGetter returns canned PPMI values or errors per pubkey.
type mockPPMIGetter struct {
	items  map[[32]byte]dhtindex.PPMIValue
	errors map[[32]byte]error
}

func (m *mockPPMIGetter) GetPPMI(_ context.Context, pubkey [32]byte) (dhtindex.PPMIValue, error) {
	if err, ok := m.errors[pubkey]; ok {
		return dhtindex.PPMIValue{}, err
	}
	if v, ok := m.items[pubkey]; ok {
		return v, nil
	}
	return dhtindex.PPMIValue{}, errors.New("no PPMI")
}

// pubkeyHex returns a 64-char hex deterministically derived from
// the full label via SHA-256 — ensures distinct labels map to
// distinct pubkeys even when labels share prefixes.
func pubkeyHex(label string) string {
	sum := sha256.Sum256([]byte(label))
	return hex.EncodeToString(sum[:])
}

func pubkeyBytes(label string) [32]byte {
	return sha256.Sum256([]byte(label))
}

func newTestLookup() *dhtindex.Lookup {
	getter := dhtindex.NewMemoryPutterGetter(nil)
	return dhtindex.NewLookup(getter)
}

func TestBootstrapConstructorValidatesAnchors(t *testing.T) {
	lookup := newTestLookup()

	// Wrong-length hex.
	opts := DefaultBootstrapOptions()
	opts.AnchorHexes = []string{"not-hex"}
	if _, err := NewBootstrap(lookup, nil, nil, nil, opts, nil); err == nil {
		t.Error("expected error for non-hex anchor")
	}

	// Wrong byte length.
	opts.AnchorHexes = []string{hex.EncodeToString(make([]byte, 20))}
	if _, err := NewBootstrap(lookup, nil, nil, nil, opts, nil); err == nil {
		t.Error("expected error for 20-byte anchor")
	}

	// Empty string is fine — skipped.
	opts.AnchorHexes = []string{"", pubkeyHex("anchor1")}
	b, err := NewBootstrap(lookup, nil, nil, nil, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.AnchorKeys()) != 1 {
		t.Errorf("empty-string anchor should be skipped, got %d keys", len(b.AnchorKeys()))
	}
}

func TestRunAnchorsFetchesAllAvailable(t *testing.T) {
	lookup := newTestLookup()
	a1 := pubkeyBytes("a")
	a2 := pubkeyBytes("b")
	a3 := pubkeyBytes("c")

	ppmi := &mockPPMIGetter{
		items: map[[32]byte]dhtindex.PPMIValue{
			a1: {IH: bytes.Repeat([]byte{0x01}, 20)},
			a2: {IH: bytes.Repeat([]byte{0x02}, 20)},
			// a3 not in items → will error.
		},
	}
	opts := DefaultBootstrapOptions()
	opts.AnchorHexes = []string{hex.EncodeToString(a1[:]), hex.EncodeToString(a2[:]), hex.EncodeToString(a3[:])}

	b, err := NewBootstrap(lookup, ppmi, nil, nil, opts, nil)
	if err != nil {
		t.Fatal(err)
	}

	succeeded, errs := b.RunAnchors(context.Background())
	if succeeded != 2 {
		t.Errorf("succeeded = %d, want 2", succeeded)
	}
	if len(errs) != 1 {
		t.Errorf("errors = %d, want 1 (a3)", len(errs))
	}
	// Lookup should have 2 admitted indexers.
	if n := len(lookup.Indexers()); n != 2 {
		t.Errorf("lookup has %d indexers, want 2", n)
	}
}

func TestRunAnchorsNoPPMIGetter(t *testing.T) {
	lookup := newTestLookup()
	opts := DefaultBootstrapOptions()
	b, err := NewBootstrap(lookup, nil, nil, nil, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, errs := b.RunAnchors(context.Background())
	if len(errs) == 0 {
		t.Error("expected an error when no PPMI getter is attached")
	}
}

func TestEndorsementThresholdAdmits(t *testing.T) {
	lookup := newTestLookup()
	opts := DefaultBootstrapOptions()
	opts.EndorsementThreshold = 2
	b, err := NewBootstrap(lookup, nil, nil, nil, opts, nil)
	if err != nil {
		t.Fatal(err)
	}

	cand := pubkeyBytes("cand")
	e1 := pubkeyBytes("end1")
	e2 := pubkeyBytes("end2")

	if b.IngestEndorsement(e1, cand) {
		t.Error("admission should not fire after 1 endorsement (threshold=2)")
	}
	if !b.IsPending(cand) {
		t.Error("candidate should be pending after first endorsement")
	}
	if !b.IngestEndorsement(e2, cand) {
		t.Error("admission should fire on second endorsement (threshold=2 without tracker)")
	}
	if !b.IsAdmitted(cand) {
		t.Error("candidate should now be admitted")
	}
	// Subsequent endorsements are idempotent.
	if !b.IngestEndorsement(e1, cand) {
		t.Error("already-admitted candidate should remain admitted")
	}
}

func TestEndorsementIdempotentOnSameEndorser(t *testing.T) {
	lookup := newTestLookup()
	opts := DefaultBootstrapOptions()
	opts.EndorsementThreshold = 2
	b, _ := NewBootstrap(lookup, nil, nil, nil, opts, nil)

	cand := pubkeyBytes("cand")
	e1 := pubkeyBytes("same")

	b.IngestEndorsement(e1, cand)
	b.IngestEndorsement(e1, cand) // same endorser again — should not cross threshold
	if b.IsAdmitted(cand) {
		t.Error("duplicate endorsements from one endorser should not reach threshold 2")
	}
}

func TestCrawlCandidateWithoutValidSigRejected(t *testing.T) {
	lookup := newTestLookup()
	b, _ := NewBootstrap(lookup, nil, nil, nil, DefaultBootstrapOptions(), nil)

	cand := pubkeyBytes("crawl")
	if b.CandidateFromCrawl(cand, false) {
		t.Fatal("candidate with invalid sig must not be admitted")
	}
	if b.IsAdmitted(cand) {
		t.Fatal("invalid-sig candidate should not be admitted")
	}
}

func TestCrawlCandidateWithoutBloomDefaultDenies(t *testing.T) {
	lookup := newTestLookup()
	// No Bloom filter → bloomPolicy is false → candidate goes
	// pending but not admitted.
	b, _ := NewBootstrap(lookup, nil, nil, nil, DefaultBootstrapOptions(), nil)

	cand := pubkeyBytes("crawl2")
	if b.CandidateFromCrawl(cand, true) {
		t.Error("without Bloom filter the crawl candidate must default-deny")
	}
}

func TestMaxTrackedPublishersCap(t *testing.T) {
	lookup := newTestLookup()
	opts := DefaultBootstrapOptions()
	opts.MaxTrackedPublishers = 2
	opts.EndorsementThreshold = 1
	b, _ := NewBootstrap(lookup, nil, nil, nil, opts, nil)

	// Three distinct candidates, each endorsed by one peer.
	e := pubkeyBytes("endorser")
	c1 := pubkeyBytes("c1")
	c2 := pubkeyBytes("c2")
	c3 := pubkeyBytes("c3")

	if !b.IngestEndorsement(e, c1) {
		t.Fatal("c1 should admit")
	}
	if !b.IngestEndorsement(e, c2) {
		t.Fatal("c2 should admit")
	}
	if b.IngestEndorsement(e, c3) {
		t.Error("c3 should be refused (cap reached)")
	}
	if b.AdmittedCount() != 2 {
		t.Errorf("admitted = %d, want 2 (capped)", b.AdmittedCount())
	}
}

func TestDefaultAnchorsConstant(t *testing.T) {
	// The hardcoded list is allowed to be empty during development,
	// but the slice must exist so operators can index into it.
	_ = DefaultAnchorPubkeys
}

func TestAdmitSetsLookupLabel(t *testing.T) {
	lookup := newTestLookup()
	a := pubkeyBytes("labeled")

	ppmi := &mockPPMIGetter{
		items: map[[32]byte]dhtindex.PPMIValue{
			a: {IH: bytes.Repeat([]byte{0x01}, 20)},
		},
	}
	opts := DefaultBootstrapOptions()
	opts.AnchorHexes = []string{hex.EncodeToString(a[:])}
	b, _ := NewBootstrap(lookup, ppmi, nil, nil, opts, nil)

	if _, errs := b.RunAnchors(context.Background()); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	found := false
	for _, info := range lookup.Indexers() {
		if info.PubKey == a {
			found = true
			if info.Label == "" {
				t.Error("admitted anchor has empty label")
			}
		}
	}
	if !found {
		t.Fatal("admitted anchor missing from Lookup.Indexers()")
	}
}
