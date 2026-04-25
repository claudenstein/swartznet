package companion

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// mkRecord synthesises a Record with a valid ed25519 signature
// over the canonical fields. Used by every subtest that needs
// real-looking leaf content.
func mkRecord(t *testing.T, pub ed25519.PublicKey, priv ed25519.PrivateKey, kw string, ih [20]byte, ts int64, pow uint64) Record {
	t.Helper()
	var r Record
	copy(r.Pk[:], pub)
	r.Kw = kw
	r.Ih = ih
	r.T = ts
	r.Pow = pow
	sig := ed25519.Sign(priv, RecordSigMessage(r))
	copy(r.Sig[:], sig)
	return r
}

func TestEncodeRecordRoundTrip(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var ih [20]byte
	copy(ih[:], bytes.Repeat([]byte{0xAB}, 20))

	orig := mkRecord(t, pub, priv, "ubuntu", ih, 1712649600, 42)

	raw, err := EncodeRecord(orig)
	if err != nil {
		t.Fatalf("EncodeRecord: %v", err)
	}
	if len(raw) == 0 || len(raw) > MaxRecordBytes {
		t.Fatalf("record %d bytes, outside (0, %d]", len(raw), MaxRecordBytes)
	}

	got, err := DecodeRecord(raw)
	if err != nil {
		t.Fatalf("DecodeRecord: %v", err)
	}
	if got.Kw != orig.Kw || got.T != orig.T || got.Pow != orig.Pow {
		t.Fatalf("fields mismatch: %+v != %+v", got, orig)
	}
	if got.Pk != orig.Pk || got.Ih != orig.Ih || got.Sig != orig.Sig {
		t.Fatal("binary fields mismatch after round-trip")
	}
	if err := VerifyRecordSig(got); err != nil {
		t.Fatalf("signature failed round-trip verification: %v", err)
	}
}

func TestEncodeRecordRejectsOversizeKeyword(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var ih [20]byte
	huge := strings.Repeat("x", MaxKeywordBytes+1)
	r := mkRecord(t, pub, priv, huge, ih, 0, 0)
	if _, err := EncodeRecord(r); err == nil {
		t.Fatal("expected error for oversize keyword")
	}
}

func TestVerifyRecordSigDetectsTamper(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var ih [20]byte
	r := mkRecord(t, pub, priv, "linux", ih, 1, 0)
	// Mutate kw; signature covered the old kw.
	r.Kw = "windows"
	if err := VerifyRecordSig(r); err == nil {
		t.Fatal("expected tampered record to fail signature verification")
	}
}

func TestLeafRoundTrip(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	records := make([]Record, 0, 5)
	for i := 0; i < 5; i++ {
		var ih [20]byte
		ih[0] = byte(i)
		records = append(records, mkRecord(t, pub, priv, "linux", ih, int64(i), 0))
	}

	const pageSize = 4096
	page, err := EncodeLeaf(0, records, pageSize)
	if err != nil {
		t.Fatalf("EncodeLeaf: %v", err)
	}
	if len(page) != pageSize {
		t.Fatalf("page %d bytes, expected pageSize %d (zero-padded)", len(page), pageSize)
	}
	if !bytes.Equal(page[0:6], BTreeMagic[:]) {
		t.Fatalf("missing magic, got %x", page[0:6])
	}

	hdr, got, err := DecodeLeaf(page)
	if err != nil {
		t.Fatalf("DecodeLeaf: %v", err)
	}
	if hdr.Kind != PageKindLeaf {
		t.Fatalf("kind = %d, want leaf", hdr.Kind)
	}
	if len(got) != len(records) {
		t.Fatalf("got %d records, want %d", len(got), len(records))
	}
	for i := range got {
		if got[i].Kw != records[i].Kw || got[i].T != records[i].T {
			t.Errorf("record %d mismatch", i)
		}
		if err := VerifyRecordSig(got[i]); err != nil {
			t.Errorf("record %d sig fails after round-trip: %v", i, err)
		}
	}
}

func TestLeafOverflowReported(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	records := make([]Record, 0, 200)
	for i := 0; i < 200; i++ {
		var ih [20]byte
		ih[0] = byte(i)
		ih[1] = byte(i >> 8)
		records = append(records, mkRecord(t, pub, priv, "a", ih, int64(i), 0))
	}
	// 200 records × ~170 bytes each is > 256 but fits in e.g. 65536.
	// Use an absurdly small pageSize so overflow is guaranteed.
	if _, err := EncodeLeaf(0, records, 256); !errors.Is(err, ErrPageOverflow) {
		t.Fatalf("want ErrPageOverflow, got %v", err)
	}
}

func TestInteriorRoundTrip(t *testing.T) {
	children := []InteriorChild{
		{Separator: []byte(""), ChildIndex: 1},
		{Separator: []byte("linux\x00" + strings.Repeat("\x00", 19)), ChildIndex: 5},
		{Separator: []byte("ubuntu\x00" + strings.Repeat("\xFF", 20)), ChildIndex: 12},
	}
	const pageSize = 4096
	page, err := EncodeInterior(PageKindRoot, 2, children, pageSize)
	if err != nil {
		t.Fatalf("EncodeInterior: %v", err)
	}

	hdr, got, err := DecodeInterior(page)
	if err != nil {
		t.Fatalf("DecodeInterior: %v", err)
	}
	if hdr.Kind != PageKindRoot {
		t.Fatalf("kind = %d, want root", hdr.Kind)
	}
	if hdr.Level != 2 {
		t.Fatalf("level = %d, want 2", hdr.Level)
	}
	if len(got) != len(children) {
		t.Fatalf("got %d children, want %d", len(got), len(children))
	}
	for i := range got {
		if got[i].ChildIndex != children[i].ChildIndex {
			t.Errorf("child %d index mismatch", i)
		}
		if !bytes.Equal(got[i].Separator, children[i].Separator) {
			t.Errorf("child %d separator mismatch", i)
		}
	}
}

func TestTrailerRoundTripAndSig(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var tr Trailer
	tr.TrailerVersion = 0x01
	copy(tr.PubKey[:], pub)
	tr.Seq = 7
	tr.CreatedTs = 1712649600
	tr.RootPieceIndex = 0
	tr.NumPages = 42
	tr.NumRecords = 12345
	tr.MinPoWBits = MinPoWBitsDefault
	copy(tr.TreeFingerprint[:], bytes.Repeat([]byte{0xAA}, 32))

	sig := ed25519.Sign(priv, TrailerSigMessage(tr))
	copy(tr.PublisherSig[:], sig)

	const pageSize = 4096
	page, err := EncodeTrailer(tr, pageSize)
	if err != nil {
		t.Fatalf("EncodeTrailer: %v", err)
	}

	got, err := DecodeTrailer(page)
	if err != nil {
		t.Fatalf("DecodeTrailer: %v", err)
	}
	if got.Seq != tr.Seq || got.NumRecords != tr.NumRecords {
		t.Fatalf("trailer scalar fields mismatch: got %+v", got)
	}
	if err := VerifyTrailerSig(got); err != nil {
		t.Fatalf("trailer signature failed: %v", err)
	}
}

func TestTrailerRejectsMutatedFingerprint(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var tr Trailer
	tr.TrailerVersion = 0x01
	copy(tr.PubKey[:], pub)
	tr.Seq = 1
	copy(tr.TreeFingerprint[:], bytes.Repeat([]byte{0x11}, 32))

	sig := ed25519.Sign(priv, TrailerSigMessage(tr))
	copy(tr.PublisherSig[:], sig)

	tr.TreeFingerprint[0] = 0xFF // post-sign tamper
	if err := VerifyTrailerSig(tr); err == nil {
		t.Fatal("expected mutated fingerprint to fail verification")
	}
}

func TestRejectBadMagic(t *testing.T) {
	var page [64]byte
	copy(page[:6], []byte("XXXXXX"))
	if _, err := decodeHeader(page[:]); err == nil {
		t.Fatal("expected bad-magic error")
	}
}

func TestRejectUnsupportedVersion(t *testing.T) {
	page := make([]byte, 64)
	copy(page[0:6], BTreeMagic[:])
	page[6] = 0xEE // unsupported version
	if _, err := decodeHeader(page); err == nil {
		t.Fatal("expected version-unsupported error")
	}
}

// TestRecordKeyOrdering documents the invariant subscriber.go will
// rely on: sort records by keyword ascending, tie-break by infohash
// ascending, and the RecordKey byte-compare reproduces that order.
func TestRecordKeyOrdering(t *testing.T) {
	var ihLo, ihHi [20]byte
	ihHi[0] = 0xFF
	cases := []struct {
		a, b Record
		less bool
	}{
		{Record{Kw: "a", Ih: ihLo}, Record{Kw: "b", Ih: ihLo}, true},
		{Record{Kw: "a", Ih: ihLo}, Record{Kw: "a", Ih: ihHi}, true},
		{Record{Kw: "ubuntu", Ih: ihLo}, Record{Kw: "ubuntu-desktop", Ih: ihLo}, true},
		{Record{Kw: "b", Ih: ihLo}, Record{Kw: "a", Ih: ihLo}, false},
	}
	for i, c := range cases {
		got := bytes.Compare(RecordKey(c.a), RecordKey(c.b)) < 0
		if got != c.less {
			t.Errorf("case %d: Compare(%s, %s) = %v, want less=%v",
				i, c.a.Kw, c.b.Kw, got, c.less)
		}
	}
}

// Sanity: the magic bytes printed by `file(1)` style tools show as
// the distinctive "SNAGG\0" prefix.
func TestBTreeMagicString(t *testing.T) {
	if got := hex.EncodeToString(BTreeMagic[:]); got != "534e41474700" {
		t.Errorf("magic = %s, want 534e41474700", got)
	}
}
