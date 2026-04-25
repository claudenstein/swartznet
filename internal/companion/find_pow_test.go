package companion

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

// TestFindEnforcesMinPoWBits exercises BTreeReader.Find's
// MinPoWBits>0 branch, which the existing Find tests skip
// (their trees use MinPoWBits=0 since unit tests can't afford
// to mine D=20 nonces).
//
// Strategy: build a tree with MinPoWBits=24 — un-mined records
// have ~2^-24 probability of clearing the threshold, so all
// records get filtered out. Find returns an empty slice rather
// than the matching records, confirming the PoW gate fires.
//
// This is the production reader path that defends against
// floods of cheap unsigned records: the trailer declares the
// minimum bits, and Find drops anything that fails to clear it.
func TestFindEnforcesMinPoWBits(t *testing.T) {
	t.Parallel()

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var pk [32]byte
	copy(pk[:], pub)
	recs := makeRecords(t, pub, priv, 16, []string{"linux"})
	out, err := BuildBTree(BuildBTreeInput{
		Records:    recs,
		PubKey:     pk,
		PrivKey:    priv,
		Seq:        1,
		PieceSize:  MinPieceSize,
		CreatedTs:  1712649600,
		MinPoWBits: 24, // ~1 in 16M records pass — 16 records will
		// virtually never have one clear it.
	})
	if err != nil {
		t.Fatalf("BuildBTree: %v", err)
	}

	r, err := OpenBTree(&BytesPageSource{Data: out.Bytes, PieceSize: MinPieceSize})
	if err != nil {
		t.Fatalf("OpenBTree: %v", err)
	}
	if r.Trailer().MinPoWBits != 24 {
		t.Fatalf("trailer.MinPoWBits = %d, want 24", r.Trailer().MinPoWBits)
	}

	got, err := r.Find("linux")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Find returned %d records — Pow gate should reject all 16 un-mined records", len(got))
	}
}

// TestFindReturnsAllAtMinPoWBitsZero — sanity sibling of the
// PoW-enforcing test: when MinPoWBits=0 the Find path must
// not touch VerifyRecordPoW at all, so every signed record
// returns. Without this baseline the rejection test above
// could falsely "pass" if Find were broken in a different way.
func TestFindReturnsAllAtMinPoWBitsZero(t *testing.T) {
	t.Parallel()

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var pk [32]byte
	copy(pk[:], pub)
	recs := makeRecords(t, pub, priv, 8, []string{"linux"})
	out, err := BuildBTree(BuildBTreeInput{
		Records:    recs,
		PubKey:     pk,
		PrivKey:    priv,
		Seq:        1,
		PieceSize:  MinPieceSize,
		CreatedTs:  1712649600,
		MinPoWBits: 0,
	})
	if err != nil {
		t.Fatalf("BuildBTree: %v", err)
	}

	r, err := OpenBTree(&BytesPageSource{Data: out.Bytes, PieceSize: MinPieceSize})
	if err != nil {
		t.Fatalf("OpenBTree: %v", err)
	}

	got, err := r.Find("linux")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	wantLinux := 0
	for _, rec := range recs {
		if rec.Kw == "linux" {
			wantLinux++
		}
	}
	if len(got) != wantLinux {
		t.Errorf("Find at MinPoWBits=0 returned %d, want %d (all signed records should pass)",
			len(got), wantLinux)
	}
}
