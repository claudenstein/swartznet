package companion

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"testing"
)

// makeRecords synthesises n records with a deterministic keyword/
// infohash pattern and valid per-record signatures. Useful for
// builder tests that need enough records to span multiple leaves.
func makeRecords(t *testing.T, pub ed25519.PublicKey, priv ed25519.PrivateKey, n int, keywords []string) []Record {
	t.Helper()
	out := make([]Record, 0, n)
	for i := 0; i < n; i++ {
		kw := keywords[i%len(keywords)]
		var ih [20]byte
		ih[0] = byte(i & 0xFF)
		ih[1] = byte((i >> 8) & 0xFF)
		ih[2] = byte((i >> 16) & 0xFF)
		var r Record
		copy(r.Pk[:], pub)
		r.Kw = kw
		r.Ih = ih
		r.T = int64(1000 + i)
		r.Pow = uint64(i)
		sig := ed25519.Sign(priv, RecordSigMessage(r))
		copy(r.Sig[:], sig)
		out = append(out, r)
	}
	return out
}

func TestBuildBTreeTinyTree(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	recs := makeRecords(t, pub, priv, 3, []string{"linux", "ubuntu"})

	var pk [32]byte
	copy(pk[:], pub)

	const pieceSize = MinPieceSize
	out, err := BuildBTree(BuildBTreeInput{
		Records:   recs,
		PubKey:    pk,
		PrivKey:   priv,
		Seq:       1,
		PieceSize: pieceSize,
	})
	if err != nil {
		t.Fatalf("BuildBTree: %v", err)
	}

	// Minimum tree: root + one leaf + trailer = 3 pages.
	if out.NumPages < 3 {
		t.Fatalf("NumPages = %d, want ≥3", out.NumPages)
	}
	if out.NumRecords != len(recs) {
		t.Fatalf("NumRecords = %d, want %d", out.NumRecords, len(recs))
	}
	if len(out.Bytes) != out.NumPages*pieceSize {
		t.Fatalf("Bytes %d, want %d", len(out.Bytes), out.NumPages*pieceSize)
	}

	// Verify piece 0 is root, last piece is trailer.
	rootPage := out.Bytes[0:pieceSize]
	hdr, children, err := DecodeInterior(rootPage)
	if err != nil {
		t.Fatalf("DecodeInterior(root): %v", err)
	}
	if hdr.Kind != PageKindRoot {
		t.Fatalf("root kind = %d, want root", hdr.Kind)
	}
	if len(children) == 0 {
		t.Fatal("root has no children")
	}

	last := out.Bytes[(out.NumPages-1)*pieceSize : out.NumPages*pieceSize]
	trailer, err := DecodeTrailer(last)
	if err != nil {
		t.Fatalf("DecodeTrailer: %v", err)
	}
	if trailer.NumRecords != uint64(len(recs)) {
		t.Fatalf("trailer NumRecords = %d, want %d", trailer.NumRecords, len(recs))
	}
	if trailer.TreeFingerprint != out.TreeFingerprint {
		t.Fatalf("trailer fingerprint != out fingerprint")
	}
	if err := VerifyTrailerSig(trailer); err != nil {
		t.Fatalf("trailer sig failed: %v", err)
	}
}

func TestBuildBTreeDeterministic(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	recs := makeRecords(t, pub, priv, 50, []string{"linux", "ubuntu", "debian"})

	var pk [32]byte
	copy(pk[:], pub)

	// Use a fixed CreatedTs to remove the only source of non-
	// determinism (clock). Without this the trailer CreatedTs
	// differs between runs.
	input := BuildBTreeInput{
		Records:   recs,
		PubKey:    pk,
		PrivKey:   priv,
		Seq:       7,
		PieceSize: MinPieceSize,
		CreatedTs: 1712649600,
	}

	a, err := BuildBTree(input)
	if err != nil {
		t.Fatal(err)
	}
	b, err := BuildBTree(input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes, b.Bytes) {
		t.Fatalf("builds not deterministic (%d vs %d bytes, fingerprint match? %v)",
			len(a.Bytes), len(b.Bytes), a.TreeFingerprint == b.TreeFingerprint)
	}
	if a.TreeFingerprint != b.TreeFingerprint {
		t.Fatal("fingerprint drifted between identical inputs")
	}
}

// Permuting input record order MUST produce the same output
// (the builder sorts internally by RecordKey).
func TestBuildBTreeSortInvariant(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	base := makeRecords(t, pub, priv, 40, []string{"a", "b", "c", "d"})

	shuffled := make([]Record, len(base))
	copy(shuffled, base)
	// simple reversal — a non-identity permutation
	for i, j := 0, len(shuffled)-1; i < j; i, j = i+1, j-1 {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	}

	var pk [32]byte
	copy(pk[:], pub)
	input := BuildBTreeInput{
		PubKey:    pk,
		PrivKey:   priv,
		Seq:       1,
		PieceSize: MinPieceSize,
		CreatedTs: 1,
	}

	input.Records = base
	a, err := BuildBTree(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Records = shuffled
	b, err := BuildBTree(input)
	if err != nil {
		t.Fatal(err)
	}
	if a.TreeFingerprint != b.TreeFingerprint {
		t.Fatal("fingerprint differs under record permutation")
	}
	if !bytes.Equal(a.Bytes, b.Bytes) {
		t.Fatal("build differs under record permutation")
	}
}

// A multi-leaf tree must decode cleanly: every leaf page yields
// its records back, every interior page's children point at valid
// page indices.
func TestBuildBTreeMultiLeafWalk(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	recs := makeRecords(t, pub, priv, 400, []string{"foo", "bar", "baz"})

	var pk [32]byte
	copy(pk[:], pub)

	out, err := BuildBTree(BuildBTreeInput{
		Records:   recs,
		PubKey:    pk,
		PrivKey:   priv,
		Seq:       1,
		PieceSize: MinPieceSize,
		CreatedTs: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Walk every page in order and count the records we retrieve
	// from leaves. Must equal NumRecords.
	pageSize := MinPieceSize
	total := 0
	for i := 0; i < out.NumPages-1; i++ { // exclude trailer
		page := out.Bytes[i*pageSize : (i+1)*pageSize]
		hdr, err := decodeHeader(page)
		if err != nil {
			t.Fatalf("page %d header: %v", i, err)
		}
		switch hdr.Kind {
		case PageKindLeaf:
			_, records, err := DecodeLeaf(page)
			if err != nil {
				t.Fatalf("leaf %d: %v", i, err)
			}
			total += len(records)
			for _, r := range records {
				if err := VerifyRecordSig(r); err != nil {
					t.Errorf("leaf %d record sig: %v", i, err)
				}
			}
		case PageKindRoot, PageKindInterior:
			_, children, err := DecodeInterior(page)
			if err != nil {
				t.Fatalf("interior %d: %v", i, err)
			}
			for _, c := range children {
				if c.ChildIndex == 0 {
					t.Errorf("page %d has a zero child index — points at root?", i)
				}
				if int(c.ChildIndex) >= out.NumPages {
					t.Errorf("page %d child index %d out of bounds", i, c.ChildIndex)
				}
			}
		default:
			t.Fatalf("page %d unexpected kind 0x%02x", i, hdr.Kind)
		}
	}
	if total != out.NumRecords {
		t.Errorf("sum of leaf records = %d, NumRecords = %d", total, out.NumRecords)
	}
}

func TestBuildBTreeRejectsEmpty(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var pk [32]byte
	copy(pk[:], pub)
	_, err := BuildBTree(BuildBTreeInput{
		Records:   nil,
		PubKey:    pk,
		PrivKey:   priv,
		Seq:       1,
		PieceSize: MinPieceSize,
	})
	if err == nil {
		t.Fatal("expected error for empty record set")
	}
}

func TestBuildBTreeRejectsBadPieceSize(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	recs := makeRecords(t, pub, priv, 3, []string{"a"})
	var pk [32]byte
	copy(pk[:], pub)

	for _, ps := range []int{0, 1024, MinPieceSize - 1, MaxPieceSize + 1} {
		_, err := BuildBTree(BuildBTreeInput{
			Records:   recs,
			PubKey:    pk,
			PrivKey:   priv,
			Seq:       1,
			PieceSize: ps,
		})
		if err == nil {
			t.Errorf("pieceSize %d should have been rejected", ps)
		}
	}
}

// Fingerprint derivation must match what a reader computes from
// the leaf records alone — equal to SHA256 of concat(EncodeRecord).
func TestBuildBTreeFingerprintMatchesCanonicalStream(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	recs := makeRecords(t, pub, priv, 20, []string{"alpha", "beta"})

	var pk [32]byte
	copy(pk[:], pub)
	out, err := BuildBTree(BuildBTreeInput{
		Records:   recs,
		PubKey:    pk,
		PrivKey:   priv,
		Seq:       1,
		PieceSize: MinPieceSize,
		CreatedTs: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Compute fingerprint the way a subscriber would: walk leaves
	// in order, hash each record's canonical bytes.
	got := canonicalFingerprint(t, out.Bytes, MinPieceSize, out.NumPages)
	if got != out.TreeFingerprint {
		t.Fatalf("fingerprint mismatch: builder %x vs reader %x",
			out.TreeFingerprint, got)
	}
}

// canonicalFingerprint derives the tree fingerprint by walking
// every leaf in piece-index order. Used as a sanity check that
// builder and reader agree on canonicality.
func canonicalFingerprint(t *testing.T, fileBytes []byte, pieceSize int, numPages int) [32]byte {
	t.Helper()
	// Collect leaves in sorted record order. Leaves appear at
	// increasing piece indices in the BFS layout; since RecordKey
	// is monotonic across leaves, reading them in piece order
	// gives records in sorted order.
	var all []Record
	for i := 0; i < numPages-1; i++ {
		page := fileBytes[i*pieceSize : (i+1)*pieceSize]
		hdr, err := decodeHeader(page)
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Kind != PageKindLeaf {
			continue
		}
		_, recs, err := DecodeLeaf(page)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, recs...)
	}
	return fingerprintFromRecords(t, all)
}

func fingerprintFromRecords(t *testing.T, records []Record) [32]byte {
	t.Helper()
	var hash [32]byte
	s := sha256.New()
	for _, r := range records {
		enc, err := EncodeRecord(r)
		if err != nil {
			t.Fatal(err)
		}
		s.Write(enc)
	}
	copy(hash[:], s.Sum(nil))
	return hash
}
