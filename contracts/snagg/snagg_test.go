package snagg

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
)

// fixedKey derives a deterministic ed25519 key from a seed byte.
func fixedKey(seed byte) (ed25519.PrivateKey, [32]byte) {
	s := bytes.Repeat([]byte{seed}, ed25519.SeedSize)
	priv := ed25519.NewKeyFromSeed(s)
	var pub [32]byte
	copy(pub[:], priv.Public().(ed25519.PublicKey))
	return priv, pub
}

// signRec builds a signed record (pow=0).
func signRec(priv ed25519.PrivateKey, pub [32]byte, kw string, ihSeed byte, t int64) Record {
	var r Record
	r.Pk = pub
	r.Kw = kw
	for i := range r.Ih {
		r.Ih[i] = ihSeed + byte(i)
	}
	r.T = t
	r.Pow = 0
	sig := ed25519.Sign(priv, r.SigMessage())
	copy(r.Sig[:], sig)
	return r
}

func TestEncodeRecordKeyOrder(t *testing.T) {
	t.Parallel()
	priv, pub := fixedKey(1)
	r := signRec(priv, pub, "ubuntu", 0x10, 1700000000)
	enc, err := EncodeRecord(r)
	if err != nil {
		t.Fatal(err)
	}
	// Keys must appear in ih < kw < pk < pow < sig < t order.
	s := string(enc)
	order := []string{"2:ih", "2:kw", "2:pk", "3:pow", "3:sig", "1:t"}
	last := -1
	for _, k := range order {
		i := bytes.Index(enc, []byte(k))
		if i < 0 {
			t.Fatalf("missing key %q in %q", k, s)
		}
		if i < last {
			t.Errorf("key %q out of canonical order", k)
		}
		last = i
	}
	back, err := DecodeRecord(enc)
	if err != nil {
		t.Fatal(err)
	}
	if back.Kw != "ubuntu" || back.Ih != r.Ih || back.Pk != r.Pk {
		t.Errorf("record round-trip mismatch")
	}
}

func TestBuildOpenFindRoundTrip(t *testing.T) {
	t.Parallel()
	priv, pub := fixedKey(2)
	var recs []Record
	for i := 0; i < 300; i++ {
		kw := fmt.Sprintf("kw%03d", i%50) // 50 distinct keywords, repeated
		recs = append(recs, signRec(priv, pub, kw, byte(i), int64(1700000000+i)))
	}
	built, err := BuildBTree(BuildInput{
		Records: recs, PubKey: pub, PrivKey: priv, Seq: 1, CreatedTs: 1700000000, MinPoWBits: 0, PieceSize: MinPieceSize,
	})
	if err != nil {
		t.Fatal(err)
	}
	if built.NumPages < 3 {
		t.Fatalf("num pages = %d, want ≥3", built.NumPages)
	}

	tree, err := OpenBTree(BytesPageSource{Data: built.Bytes, PieceSize: built.PieceSize})
	if err != nil {
		t.Fatalf("OpenBTree: %v", err)
	}
	if err := tree.VerifyFingerprint(); err != nil {
		t.Fatalf("VerifyFingerprint: %v", err)
	}
	// Prefix query for one keyword returns exactly its records (6 per keyword:
	// 300 records / 50 keywords).
	hits, err := tree.Find("kw007")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 6 {
		t.Errorf("Find(kw007) = %d hits, want 6", len(hits))
	}
	for _, h := range hits {
		if h.Kw != "kw007" {
			t.Errorf("hit keyword %q, want kw007", h.Kw)
		}
	}
	// A broad prefix returns everything.
	all, err := tree.Find("kw")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 300 {
		t.Errorf("Find(kw) = %d, want 300", len(all))
	}
}

func TestSingleLeafTreeHasRoot(t *testing.T) {
	t.Parallel()
	priv, pub := fixedKey(3)
	recs := []Record{signRec(priv, pub, "solo", 0x20, 1700000000)}
	built, err := BuildBTree(BuildInput{Records: recs, PubKey: pub, PrivKey: priv, Seq: 1, CreatedTs: 1700000000, PieceSize: MinPieceSize})
	if err != nil {
		t.Fatal(err)
	}
	if built.NumPages != 3 { // root + leaf + trailer
		t.Fatalf("single-record tree num pages = %d, want 3", built.NumPages)
	}
	tree, err := OpenBTree(BytesPageSource{Data: built.Bytes, PieceSize: built.PieceSize})
	if err != nil {
		t.Fatal(err)
	}
	if hits, _ := tree.Find("solo"); len(hits) != 1 {
		t.Errorf("Find(solo) = %d, want 1", len(hits))
	}
}

func TestOpenRejectsBadTrailerSig(t *testing.T) {
	t.Parallel()
	priv, pub := fixedKey(4)
	recs := []Record{signRec(priv, pub, "abc", 0x30, 1700000000)}
	built, _ := BuildBTree(BuildInput{Records: recs, PubKey: pub, PrivKey: priv, Seq: 1, CreatedTs: 1700000000, PieceSize: MinPieceSize})
	// Corrupt a signature byte in the trailer page (last piece).
	data := append([]byte(nil), built.Bytes...)
	trailerOff := (built.NumPages - 1) * built.PieceSize
	data[trailerOff+PageHeaderSize+98] ^= 0xFF // first sig byte
	if _, err := OpenBTree(BytesPageSource{Data: data, PieceSize: built.PieceSize}); err == nil {
		t.Fatal("OpenBTree accepted a tampered trailer signature")
	}
}

func TestFindDropsRecordWithBadSig(t *testing.T) {
	t.Parallel()
	priv, pub := fixedKey(5)
	good := signRec(priv, pub, "keyword", 0x40, 1700000000)
	bad := signRec(priv, pub, "keyword", 0x50, 1700000000)
	bad.Sig[0] ^= 0xFF // break the signature (fingerprint still covers these bytes)
	built, _ := BuildBTree(BuildInput{Records: []Record{good, bad}, PubKey: pub, PrivKey: priv, Seq: 1, CreatedTs: 1700000000, PieceSize: MinPieceSize})
	tree, _ := OpenBTree(BytesPageSource{Data: built.Bytes, PieceSize: built.PieceSize})
	hits, err := tree.Find("keyword")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Errorf("Find returned %d hits, want 1 (bad-sig record dropped)", len(hits))
	}
}

// TestGoldenFingerprint pins the fingerprint (the PPMI commit / cross-impl
// anchor) for a fixed record set.
func TestGoldenFingerprint(t *testing.T) {
	t.Parallel()
	priv, pub := fixedKey(9)
	recs := []Record{
		signRec(priv, pub, "ubuntu", 0x01, 1700000000),
		signRec(priv, pub, "debian", 0x02, 1700000001),
	}
	built, err := BuildBTree(BuildInput{Records: recs, PubKey: pub, PrivKey: priv, Seq: 7, CreatedTs: 1700000000, MinPoWBits: 0, PieceSize: MinPieceSize})
	if err != nil {
		t.Fatal(err)
	}
	// The fingerprint is deterministic for a fixed record set (independent of
	// pieceSize/seq/createdTs). Re-derive and compare to the trailer.
	tree, _ := OpenBTree(BytesPageSource{Data: built.Bytes, PieceSize: built.PieceSize})
	if tree.Trailer.Fingerprint != built.Fingerprint {
		t.Fatal("trailer/built fingerprint disagree")
	}
	// Independently recompute: SHA256(EncodeRecord(debian) || EncodeRecord(ubuntu))
	// — sorted by RecordKey ("debian" < "ubuntu").
	h := sha256.New()
	for _, r := range []Record{recs[1], recs[0]} {
		enc, _ := EncodeRecord(r)
		h.Write(enc)
	}
	var want [32]byte
	copy(want[:], h.Sum(nil))
	if built.Fingerprint != want {
		t.Errorf("fingerprint = %s, want %s", hex.EncodeToString(built.Fingerprint[:]), hex.EncodeToString(want[:]))
	}
}

// TestGoldenTrailerLayout pins the 162-byte trailer field offsets.
func TestGoldenTrailerLayout(t *testing.T) {
	t.Parallel()
	priv, pub := fixedKey(9)
	built, _ := BuildBTree(BuildInput{
		Records: []Record{signRec(priv, pub, "x", 0x01, 1)}, PubKey: pub, PrivKey: priv,
		Seq: 0x1122334455667788, CreatedTs: 1700000000, MinPoWBits: 20, PieceSize: MinPieceSize,
	})
	trailerPage := built.Bytes[(built.NumPages-1)*built.PieceSize:]
	tr, err := DecodeTrailer(trailerPage)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Version != 1 || tr.Seq != 0x1122334455667788 || tr.CreatedTs != 1700000000 ||
		tr.RootPieceIndex != 0 || tr.NumRecords != 1 || tr.MinPoWBits != 20 {
		t.Fatalf("trailer fields = %+v", tr)
	}
	if tr.NumPages != uint32(built.NumPages) {
		t.Errorf("trailer NumPages=%d, built=%d", tr.NumPages, built.NumPages)
	}
	if err := VerifyTrailerSig(tr); err != nil {
		t.Errorf("trailer sig: %v", err)
	}
	// The signed preimage is exactly the first 98 payload bytes.
	if len(TrailerSigMessage(tr)) != 98 {
		t.Errorf("sign preimage = %d bytes, want 98", len(TrailerSigMessage(tr)))
	}
}

// TestMagicAndConstantsFrozen pins the format constants.
func TestMagicAndConstantsFrozen(t *testing.T) {
	t.Parallel()
	if hex.EncodeToString(BTreeMagic[:]) != "534e41474700" {
		t.Errorf("magic drift: %x", BTreeMagic)
	}
	if PageHeaderSize != 16 || TrailerPayloadSize != 162 || MinPieceSize != 16384 || MaxKeywordBytes != 64 || MaxRecordBytes != 256 {
		t.Error("a frozen constant changed")
	}
}
