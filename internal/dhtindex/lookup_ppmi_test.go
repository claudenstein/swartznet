package dhtindex

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

// ppmiMockGetter returns pre-canned PPMI results, one per pubkey.
// Covers both "this publisher has a PPMI" and "not found" cases.
type ppmiMockGetter struct {
	items  map[[32]byte]PPMIValue
	errors map[[32]byte]error
}

func (m *ppmiMockGetter) GetPPMI(_ context.Context, pubkey [32]byte) (PPMIValue, error) {
	if err, ok := m.errors[pubkey]; ok {
		return PPMIValue{}, err
	}
	if v, ok := m.items[pubkey]; ok {
		return v, nil
	}
	return PPMIValue{}, errNotFound
}

var errNotFound = errorString("not found")

type errorString string

func (e errorString) Error() string { return string(e) }

// WithoutPPMIGetterBehavesAsLegacy: when no PPMIGetter is attached,
// Query is identical to the pre-P2.3 path (no PPMIsResolved
// field populated, no PPMI fan-out).
func TestLookupWithoutPPMIGetterBehavesAsLegacy(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	mem := NewMemoryPutterGetter(priv)

	// Publish a legacy item.
	var ih [20]byte
	copy(ih[:], bytes.Repeat([]byte{0x01}, 20))
	kv := KeywordValue{Hits: []KeywordHit{{IH: ih[:], N: "ubuntu"}}}
	salt, _ := SaltForKeyword("ubuntu")
	if err := mem.Put(context.Background(), salt, kv); err != nil {
		t.Fatal(err)
	}

	lookup := NewLookup(mem)
	lookup.AddIndexer(mem.pub, "legacy-publisher")

	resp, err := lookup.Query(context.Background(), "ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Hits) != 1 {
		t.Fatalf("Hits = %d, want 1", len(resp.Hits))
	}
	if len(resp.PPMIsResolved) != 0 {
		t.Errorf("PPMIsResolved should be empty without getter, got %d", len(resp.PPMIsResolved))
	}
	if resp.PPMIMissing != 0 {
		t.Errorf("PPMIMissing = %d, want 0", resp.PPMIMissing)
	}
}

// WithPPMIGetter resolves publishers who have PPMIs and falls back
// to the legacy path for publishers who don't.
func TestLookupMixedPPMIAndLegacy(t *testing.T) {
	_, privA, _ := ed25519.GenerateKey(rand.Reader)
	_, privB, _ := ed25519.GenerateKey(rand.Reader)
	memA := NewMemoryPutterGetter(privA)
	memB := NewMemoryPutterGetter(privB)

	// Publisher A has migrated to PPMI; B is still on the legacy
	// per-keyword path.
	var ihA [20]byte
	copy(ihA[:], bytes.Repeat([]byte{0xAA}, 20))
	aPPMI := PPMIValue{IH: ihA[:], Ts: 1000}
	if err := memA.PutPPMI(context.Background(), aPPMI); err != nil {
		t.Fatal(err)
	}

	var ihB [20]byte
	copy(ihB[:], bytes.Repeat([]byte{0xBB}, 20))
	bKV := KeywordValue{Hits: []KeywordHit{{IH: ihB[:], N: "ubuntu"}}}
	salt, _ := SaltForKeyword("ubuntu")
	if err := memB.Put(context.Background(), salt, bKV); err != nil {
		t.Fatal(err)
	}

	// PPMI getter that returns A's PPMI, not found for B.
	ppmiMock := &ppmiMockGetter{
		items: map[[32]byte]PPMIValue{memA.pub: aPPMI},
	}

	// Legacy getter needs to route each pubkey to the right memory
	// store. We'll use a multiplexing getter for this test.
	legacyMock := &multiGetter{
		byPub: map[[32]byte]Getter{
			memA.pub: memA,
			memB.pub: memB,
		},
	}

	lookup := NewLookup(legacyMock)
	lookup.AddIndexer(memA.pub, "migrated")
	lookup.AddIndexer(memB.pub, "legacy")
	lookup.SetPPMIGetter(ppmiMock)

	resp, err := lookup.Query(context.Background(), "ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.PPMIsResolved) != 1 {
		t.Fatalf("PPMIsResolved = %d, want 1", len(resp.PPMIsResolved))
	}
	if resp.PPMIsResolved[0].PubKey != memA.pub {
		t.Errorf("resolved PPMI pubkey mismatch")
	}
	if !bytes.Equal(resp.PPMIsResolved[0].Value.IH, ihA[:]) {
		t.Errorf("resolved PPMI ih mismatch")
	}
	if resp.PPMIMissing != 1 {
		t.Errorf("PPMIMissing = %d, want 1 (B did not respond)", resp.PPMIMissing)
	}
	// B's legacy result should show up in Hits.
	if len(resp.Hits) != 1 {
		t.Fatalf("Hits = %d, want 1 (B's legacy hit)", len(resp.Hits))
	}
	if resp.Hits[0].Name != "ubuntu" {
		t.Errorf("legacy hit name = %q, want ubuntu", resp.Hits[0].Name)
	}
	// Indexers-asked reports total fan-out (PPMI + legacy).
	if resp.IndexersAsked != 2 {
		t.Errorf("IndexersAsked = %d, want 2", resp.IndexersAsked)
	}
}

// When every publisher has a PPMI, the legacy fallback is empty
// and Hits is empty too.
func TestLookupAllPPMIsResolved(t *testing.T) {
	_, privA, _ := ed25519.GenerateKey(rand.Reader)
	_, privB, _ := ed25519.GenerateKey(rand.Reader)
	memA := NewMemoryPutterGetter(privA)
	memB := NewMemoryPutterGetter(privB)

	var ihA, ihB [20]byte
	copy(ihA[:], bytes.Repeat([]byte{0x11}, 20))
	copy(ihB[:], bytes.Repeat([]byte{0x22}, 20))

	ppmiMock := &ppmiMockGetter{
		items: map[[32]byte]PPMIValue{
			memA.pub: {IH: ihA[:]},
			memB.pub: {IH: ihB[:]},
		},
	}
	legacyMock := &multiGetter{byPub: map[[32]byte]Getter{memA.pub: memA, memB.pub: memB}}

	lookup := NewLookup(legacyMock)
	lookup.AddIndexer(memA.pub, "a")
	lookup.AddIndexer(memB.pub, "b")
	lookup.SetPPMIGetter(ppmiMock)

	resp, err := lookup.Query(context.Background(), "anything")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.PPMIsResolved) != 2 {
		t.Errorf("PPMIsResolved = %d, want 2", len(resp.PPMIsResolved))
	}
	if resp.PPMIMissing != 0 {
		t.Errorf("PPMIMissing = %d, want 0", resp.PPMIMissing)
	}
	if len(resp.Hits) != 0 {
		t.Errorf("Hits = %d, want 0 (all publishers covered by PPMI)", len(resp.Hits))
	}
}

// When no publisher has a PPMI, all fall back to legacy and
// PPMIMissing equals the total.
func TestLookupNoPPMIs(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	mem := NewMemoryPutterGetter(priv)

	var ih [20]byte
	copy(ih[:], bytes.Repeat([]byte{0xCC}, 20))
	kv := KeywordValue{Hits: []KeywordHit{{IH: ih[:], N: "thing"}}}
	salt, _ := SaltForKeyword("thing")
	if err := mem.Put(context.Background(), salt, kv); err != nil {
		t.Fatal(err)
	}

	ppmiMock := &ppmiMockGetter{items: map[[32]byte]PPMIValue{}}

	lookup := NewLookup(mem)
	lookup.AddIndexer(mem.pub, "legacy")
	lookup.SetPPMIGetter(ppmiMock)

	resp, err := lookup.Query(context.Background(), "thing")
	if err != nil {
		t.Fatal(err)
	}
	if resp.PPMIMissing != 1 {
		t.Errorf("PPMIMissing = %d, want 1", resp.PPMIMissing)
	}
	if len(resp.Hits) != 1 {
		t.Errorf("Hits = %d, want 1 (legacy)", len(resp.Hits))
	}
}

// SetPPMIGetter(nil) disables the Aggregate path cleanly.
func TestLookupUnsetPPMIGetter(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	mem := NewMemoryPutterGetter(priv)
	lookup := NewLookup(mem)

	var ih [20]byte
	ppmiMock := &ppmiMockGetter{items: map[[32]byte]PPMIValue{mem.pub: {IH: ih[:]}}}
	lookup.SetPPMIGetter(ppmiMock)
	if lookup.PPMIGetter() == nil {
		t.Fatal("expected PPMI getter to be set")
	}
	lookup.SetPPMIGetter(nil)
	if lookup.PPMIGetter() != nil {
		t.Fatal("expected PPMI getter to be nil after unset")
	}
}

// multiGetter routes per-keyword Get calls to different memory
// backends based on the requested pubkey. Lets tests simulate
// heterogeneous publisher state without stubbing the DHT.
type multiGetter struct {
	byPub map[[32]byte]Getter
}

func (m *multiGetter) Get(ctx context.Context, pubkey [32]byte, salt []byte) (KeywordValue, error) {
	if g, ok := m.byPub[pubkey]; ok {
		return g.Get(ctx, pubkey, salt)
	}
	return KeywordValue{}, errNotFound
}
