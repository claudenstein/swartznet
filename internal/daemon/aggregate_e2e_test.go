package daemon

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/swartznet/swartznet/internal/companion"
	"github.com/swartznet/swartznet/internal/dhtindex"
)

// TestAggregateEndToEnd exercises the full publisher → PPMI →
// subscriber → prefix-query flow across all packages touched by
// the "Aggregate" redesign. A single green run of this test is
// the smoke signal that the phased implementation hangs together:
//
//   - companion.BuildBTree produces a signed index with the PPMI
//     commit fingerprint;
//   - dhtindex.PPMIPutter stores the pointer;
//   - daemon.Bootstrap treats the publisher as an anchor and
//     admits it to the Lookup set;
//   - dhtindex.Lookup.Query returns the PPMI via the Aggregate
//     path (not the legacy fallback);
//   - companion.BTreeReader opens the index (via an in-memory
//     PageSource standing in for a seeded torrent), verifies the
//     trailer sig, then returns records matching a prefix;
//   - the found records' ed25519 signatures verify against the
//     publisher's embedded pubkey.
//
// No hashcash mining: we build the tree with MinPoWBits = 0 so
// the subscriber's VerifyRecordPoW is a no-op. A P5.1 test
// already covers the mining+verification path in isolation.
func TestAggregateEndToEnd(t *testing.T) {
	// === Publisher side ===
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var pk [32]byte
	copy(pk[:], pub)

	records := makeRealRecords(t, pub, priv, []string{"linux", "ubuntu", "debian"}, 30)

	const pieceSize = companion.MinPieceSize
	built, err := companion.BuildBTree(companion.BuildBTreeInput{
		Records:   records,
		PubKey:    pk,
		PrivKey:   priv,
		Seq:       1,
		PieceSize: pieceSize,
		CreatedTs: 1712649600,
	})
	if err != nil {
		t.Fatalf("BuildBTree: %v", err)
	}

	// Publisher mints a companion torrent and a PPMI pointer.
	// In production the torrent goes through metainfo.BuildFromFilePath
	// and the engine adds it; here we synthesise a 20-byte infohash
	// from the tree fingerprint and plumb it through.
	var ih [20]byte
	copy(ih[:], built.TreeFingerprint[:20])

	ppmi := dhtindex.PPMIValue{
		IH:     ih[:],
		Commit: built.TreeFingerprint[:],
		Ts:     1712649700,
	}

	// === DHT layer: store the PPMI ===
	mem := dhtindex.NewMemoryPutterGetter(priv)
	if err := mem.PutPPMI(context.Background(), ppmi); err != nil {
		t.Fatalf("PutPPMI: %v", err)
	}

	// === Daemon / Bootstrap side ===
	lookup := dhtindex.NewLookup(mem)
	lookup.SetPPMIGetter(mem)

	opts := DefaultBootstrapOptions()
	opts.AnchorHexes = []string{hex.EncodeToString(pk[:])}

	boot, err := NewBootstrap(lookup, mem, nil, nil, opts, nil)
	if err != nil {
		t.Fatalf("NewBootstrap: %v", err)
	}

	succeeded, errs := boot.RunAnchors(context.Background())
	if len(errs) != 0 {
		t.Fatalf("RunAnchors errors: %v", errs)
	}
	if succeeded != 1 {
		t.Fatalf("anchors succeeded = %d, want 1", succeeded)
	}
	if !boot.IsAdmitted(pk) {
		t.Fatal("publisher not admitted after successful anchor fetch")
	}

	// === Lookup-side Query: should hit the PPMI path ===
	resp, err := lookup.Query(context.Background(), "ubuntu")
	if err != nil {
		t.Fatalf("Lookup.Query: %v", err)
	}
	if len(resp.PPMIsResolved) != 1 {
		t.Fatalf("PPMIsResolved = %d, want 1", len(resp.PPMIsResolved))
	}
	got := resp.PPMIsResolved[0]
	if got.PubKey != pk {
		t.Errorf("PPMI pubkey mismatch")
	}
	if !bytes.Equal(got.Value.Commit, built.TreeFingerprint[:]) {
		t.Errorf("PPMI commit ≠ builder fingerprint")
	}
	if resp.PPMIMissing != 0 {
		t.Errorf("PPMIMissing = %d, want 0", resp.PPMIMissing)
	}

	// === Subscriber side: open the tree and run a prefix query ===
	// Stand in for a seeded torrent with an in-memory PageSource.
	src := &companion.BytesPageSource{Data: built.Bytes, PieceSize: pieceSize}
	reader, err := companion.OpenBTree(src)
	if err != nil {
		t.Fatalf("OpenBTree: %v", err)
	}
	if reader.Trailer().TreeFingerprint != built.TreeFingerprint {
		t.Fatal("reader trailer fingerprint ≠ builder fingerprint")
	}

	hits, err := reader.Find("ubuntu")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}

	// Count how many records have kw="ubuntu" in the original set.
	want := 0
	for _, r := range records {
		if r.Kw == "ubuntu" {
			want++
		}
	}
	if len(hits) != want {
		t.Fatalf("hits = %d, want %d", len(hits), want)
	}
	for _, h := range hits {
		if h.Kw != "ubuntu" {
			t.Errorf("unexpected kw %q in results", h.Kw)
		}
		if err := companion.VerifyRecordSig(h); err != nil {
			t.Errorf("record sig fails: %v", err)
		}
	}

	// === Tree fingerprint round-trip ===
	if err := reader.VerifyFingerprint(); err != nil {
		t.Fatalf("VerifyFingerprint: %v", err)
	}
}

// TestAggregateMixedMigration exercises the case where Publisher A
// has migrated to PPMI and Publisher B is still on the legacy
// per-keyword path. Proves dual-read (P2.3) covers both without
// either missing records.
func TestAggregateMixedMigration(t *testing.T) {
	_, privA, _ := ed25519.GenerateKey(rand.Reader)
	_, privB, _ := ed25519.GenerateKey(rand.Reader)

	memA := dhtindex.NewMemoryPutterGetter(privA)
	memB := dhtindex.NewMemoryPutterGetter(privB)

	// A publishes PPMI.
	var ihA [20]byte
	copy(ihA[:], bytes.Repeat([]byte{0xAA}, 20))
	if err := memA.PutPPMI(context.Background(), dhtindex.PPMIValue{IH: ihA[:]}); err != nil {
		t.Fatal(err)
	}

	// B publishes legacy per-keyword item.
	var ihB [20]byte
	copy(ihB[:], bytes.Repeat([]byte{0xBB}, 20))
	legacy := dhtindex.KeywordValue{
		Hits: []dhtindex.KeywordHit{{IH: ihB[:], N: "ubuntu-legacy"}},
	}
	saltB, _ := dhtindex.SaltForKeyword("ubuntu")
	if err := memB.Put(context.Background(), saltB, legacy); err != nil {
		t.Fatal(err)
	}

	// Lookup wired against both memories + PPMI getter pointing only
	// at A (B doesn't respond to PPMI gets).
	routedGetter := &multiGetter{
		byPub: map[[32]byte]dhtindex.Getter{
			memA.PubKey(): memA,
			memB.PubKey(): memB,
		},
	}
	routedPPMI := &multiPPMIGetter{
		byPub: map[[32]byte]dhtindex.PPMIGetter{
			memA.PubKey(): memA,
			// B deliberately omitted: its PPMI fetch fails
		},
	}
	lookup := dhtindex.NewLookup(routedGetter)
	lookup.AddIndexer(memA.PubKey(), "a-migrated")
	lookup.AddIndexer(memB.PubKey(), "b-legacy")
	lookup.SetPPMIGetter(routedPPMI)

	resp, err := lookup.Query(context.Background(), "ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	// A resolved via PPMI.
	if len(resp.PPMIsResolved) != 1 {
		t.Fatalf("PPMIsResolved = %d, want 1", len(resp.PPMIsResolved))
	}
	// B returned via legacy.
	if len(resp.Hits) != 1 {
		t.Fatalf("Hits = %d, want 1", len(resp.Hits))
	}
	if resp.Hits[0].Name != "ubuntu-legacy" {
		t.Errorf("legacy hit name = %q, want ubuntu-legacy", resp.Hits[0].Name)
	}
	if resp.PPMIMissing != 1 {
		t.Errorf("PPMIMissing = %d, want 1 (B didn't respond)", resp.PPMIMissing)
	}
}

// makeRealRecords is a copy of the helper in companion_test but
// lives here because test helpers don't cross packages.
func makeRealRecords(t *testing.T, pub ed25519.PublicKey, priv ed25519.PrivateKey, kws []string, n int) []companion.Record {
	t.Helper()
	out := make([]companion.Record, 0, n)
	for i := 0; i < n; i++ {
		kw := kws[i%len(kws)]
		var r companion.Record
		copy(r.Pk[:], pub)
		r.Kw = kw
		r.Ih[0] = byte(i)
		r.Ih[1] = byte(i >> 8)
		r.T = int64(i)
		sig := ed25519.Sign(priv, companion.RecordSigMessage(r))
		copy(r.Sig[:], sig)
		out = append(out, r)
	}
	return out
}

// multiGetter routes legacy Get calls per-pubkey.
type multiGetter struct {
	byPub map[[32]byte]dhtindex.Getter
}

func (m *multiGetter) Get(ctx context.Context, pubkey [32]byte, salt []byte) (dhtindex.KeywordValue, error) {
	if g, ok := m.byPub[pubkey]; ok {
		return g.Get(ctx, pubkey, salt)
	}
	return dhtindex.KeywordValue{}, fmt.Errorf("no getter for pubkey %x", pubkey[:8])
}

// multiPPMIGetter routes PPMI gets per-pubkey.
type multiPPMIGetter struct {
	byPub map[[32]byte]dhtindex.PPMIGetter
}

func (m *multiPPMIGetter) GetPPMI(ctx context.Context, pubkey [32]byte) (dhtindex.PPMIValue, error) {
	if g, ok := m.byPub[pubkey]; ok {
		return g.GetPPMI(ctx, pubkey)
	}
	return dhtindex.PPMIValue{}, fmt.Errorf("no PPMI getter for pubkey %x", pubkey[:8])
}
