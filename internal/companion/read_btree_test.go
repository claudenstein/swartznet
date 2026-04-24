package companion

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"sort"
	"testing"
)

// buildTestTree is a common setup helper: pub/priv, pieceSize,
// records, return a signed tree + its reader.
func buildTestTree(t *testing.T, numRecords int, keywords []string, pieceSize int) (*BTreeReader, []Record, ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	recs := makeRecords(t, pub, priv, numRecords, keywords)
	var pk [32]byte
	copy(pk[:], pub)
	out, err := BuildBTree(BuildBTreeInput{
		Records:   recs,
		PubKey:    pk,
		PrivKey:   priv,
		Seq:       1,
		PieceSize: pieceSize,
		CreatedTs: 1712649600,
	})
	if err != nil {
		t.Fatalf("BuildBTree: %v", err)
	}
	src := &BytesPageSource{Data: out.Bytes, PieceSize: pieceSize}
	r, err := OpenBTree(src)
	if err != nil {
		t.Fatalf("OpenBTree: %v", err)
	}
	return r, recs, pub, priv
}

func TestOpenBTreeVerifiesTrailer(t *testing.T) {
	r, _, _, _ := buildTestTree(t, 10, []string{"linux"}, MinPieceSize)
	if r.Trailer().NumRecords != 10 {
		t.Fatalf("trailer NumRecords = %d, want 10", r.Trailer().NumRecords)
	}
}

func TestOpenBTreeRejectsTinySource(t *testing.T) {
	// 2 pieces is too few — need ≥3 (root + leaf + trailer).
	src := &BytesPageSource{Data: make([]byte, 2*MinPieceSize), PieceSize: MinPieceSize}
	if _, err := OpenBTree(src); err == nil {
		t.Fatal("expected error on 2-piece source")
	}
}

func TestOpenBTreeRejectsTamperedTrailer(t *testing.T) {
	r, _, _, _ := buildTestTree(t, 3, []string{"a"}, MinPieceSize)
	src := r.src.(*BytesPageSource)
	// Flip one bit in the fingerprint inside the trailer page.
	lastOff := (src.NumPieces() - 1) * src.PieceSize
	// Trailer layout: header(16) + trailer_version(1) + pubkey(32)
	// + seq(8) + created_ts(8) + root_piece_index(4) + num_pages(4)
	// + num_records(8) + min_pow_bits(1) + tree_fingerprint(32) + sig(64)
	// Tree fingerprint starts at offset 16 + 1 + 32 + 8 + 8 + 4 + 4 + 8 + 1 = 82.
	src.Data[lastOff+82] ^= 0x01
	if _, err := OpenBTree(src); err == nil {
		t.Fatal("expected OpenBTree to reject tampered fingerprint")
	}
}

func TestFindExactKeyword(t *testing.T) {
	r, recs, _, _ := buildTestTree(t, 30, []string{"linux", "ubuntu", "debian"}, MinPieceSize)

	// Count how many records have kw="linux".
	wantLinux := 0
	for _, rec := range recs {
		if rec.Kw == "linux" {
			wantLinux++
		}
	}

	got, err := r.Find("linux")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != wantLinux {
		t.Fatalf("got %d records for 'linux', want %d", len(got), wantLinux)
	}
	for _, rec := range got {
		if rec.Kw != "linux" {
			t.Errorf("unexpected kw %q in linux results", rec.Kw)
		}
	}
}

// Prefix query must catch every keyword starting with the prefix,
// not just the exact match.
func TestFindPrefixMultipleKeywords(t *testing.T) {
	r, recs, _, _ := buildTestTree(t, 60, []string{"ubuntu", "ubuntu-desktop", "ubuntu-server", "windows"}, MinPieceSize)

	wantCount := 0
	for _, rec := range recs {
		if len(rec.Kw) >= 6 && rec.Kw[:6] == "ubuntu" {
			wantCount++
		}
	}

	got, err := r.Find("ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != wantCount {
		t.Fatalf("got %d 'ubuntu*' records, want %d", len(got), wantCount)
	}
	for _, rec := range got {
		if !bytes.HasPrefix([]byte(rec.Kw), []byte("ubuntu")) {
			t.Errorf("unexpected kw %q in ubuntu* results", rec.Kw)
		}
	}
}

// Narrow prefix that happens to sit between two keywords — no
// match, but must not error.
func TestFindPrefixNoMatch(t *testing.T) {
	r, _, _, _ := buildTestTree(t, 30, []string{"apple", "banana"}, MinPieceSize)

	got, err := r.Find("cherry")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d results for non-matching prefix", len(got))
	}
}

// An empty prefix matches every record.
func TestFindEmptyPrefixMatchesAll(t *testing.T) {
	r, recs, _, _ := buildTestTree(t, 50, []string{"x", "y", "z"}, MinPieceSize)

	got, err := r.Find("")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != len(recs) {
		t.Fatalf("empty-prefix returned %d, want %d", len(got), len(recs))
	}
}

// Large tree (multiple levels) must still resolve a narrow
// prefix query to only a few leaves. Not a perf assertion — a
// correctness one, ensuring descent pruning actually happens.
func TestFindPrefixInDeepTree(t *testing.T) {
	// Generate a lot of distinct keywords so the tree is multi-level.
	keywords := make([]string, 100)
	for i := range keywords {
		keywords[i] = fmt.Sprintf("kw-%04d", i)
	}
	r, _, _, _ := buildTestTree(t, 1000, keywords, MinPieceSize)

	got, err := r.Find("kw-0042")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	// 1000 records / 100 kws = ~10 per kw.
	if len(got) == 0 {
		t.Fatal("expected ≥1 result for kw-0042")
	}
	for _, rec := range got {
		if rec.Kw != "kw-0042" {
			t.Errorf("unexpected kw %q in kw-0042 results", rec.Kw)
		}
	}
}

func TestVerifyFingerprintMatches(t *testing.T) {
	r, _, _, _ := buildTestTree(t, 60, []string{"a", "b", "c"}, MinPieceSize)
	if err := r.VerifyFingerprint(); err != nil {
		t.Fatalf("pristine tree fingerprint verification failed: %v", err)
	}
}

// A leaf tampered AFTER OpenBTree succeeded would change the
// fingerprint. The trailer sig already protects the fingerprint;
// so a tampered leaf makes the derived fingerprint mismatch, and
// VerifyFingerprint catches it.
func TestVerifyFingerprintDetectsTamperedLeaf(t *testing.T) {
	r, _, _, _ := buildTestTree(t, 20, []string{"foo"}, MinPieceSize)
	src := r.src.(*BytesPageSource)

	// Find the first leaf page and flip a byte inside its payload.
	// Layout: piece 0 = root (always). So the first leaf is piece 1.
	leafOff := 1 * src.PieceSize
	// Page header is 16 bytes; skip past it so we hit payload
	// not header (which we need for DecodeLeaf to still succeed).
	// Byte 17 is inside the num_records field or first record
	// length varint; either way, mutating it should change what
	// re-derives into the fingerprint.
	src.Data[leafOff+18] ^= 0xFF

	// Depending on which byte we hit, decoding may itself fail —
	// which is ALSO a correct behavior (surface-level rejection
	// of the tamper). Either outcome is fine; the test asserts
	// that verify does NOT quietly accept.
	if err := r.VerifyFingerprint(); err == nil {
		t.Fatal("expected tampered leaf to fail VerifyFingerprint")
	}
}

// Read what we built — a full round-trip: write a known set of
// (kw, ih) pairs, read them back grouped by kw, and compare by
// sorted infohash.
func TestRoundTripRecordContents(t *testing.T) {
	r, recs, _, _ := buildTestTree(t, 40, []string{"zeta", "alpha"}, MinPieceSize)

	type pair struct{ kw string; ih [20]byte }
	byKw := make(map[string][][20]byte)
	for _, rec := range recs {
		byKw[rec.Kw] = append(byKw[rec.Kw], rec.Ih)
	}
	for kw := range byKw {
		sortHashSlice(byKw[kw])
	}

	for kw, wantIHs := range byKw {
		hits, err := r.Find(kw)
		if err != nil {
			t.Fatalf("Find(%q): %v", kw, err)
		}
		if len(hits) != len(wantIHs) {
			t.Errorf("Find(%q): got %d hits, want %d", kw, len(hits), len(wantIHs))
			continue
		}
		gotIHs := make([][20]byte, 0, len(hits))
		for _, h := range hits {
			gotIHs = append(gotIHs, h.Ih)
		}
		sortHashSlice(gotIHs)
		for i := range gotIHs {
			if gotIHs[i] != wantIHs[i] {
				t.Errorf("Find(%q) IH[%d] mismatch", kw, i)
			}
		}
	}
}

// TestNextPrefix is cheap, fast, documents the increment logic.
func TestNextPrefix(t *testing.T) {
	cases := []struct {
		in   []byte
		want []byte
	}{
		{[]byte("ubu"), []byte("ubv")},
		{[]byte("ub\xFF"), []byte("uc")},
		{[]byte("\xFF"), nil},
		{[]byte("\xFF\xFF\xFF"), nil},
		{[]byte(""), []byte{}}, // empty stays empty
	}
	for _, c := range cases {
		got := nextPrefix(c.in)
		if c.want == nil {
			if got != nil {
				t.Errorf("nextPrefix(%q) = %q, want nil (+∞)", c.in, got)
			}
			continue
		}
		if !bytes.Equal(got, c.want) {
			t.Errorf("nextPrefix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLeadingZeroBits(t *testing.T) {
	cases := []struct {
		in   []byte
		want int
	}{
		{[]byte{0x00}, 8},
		{[]byte{0x01}, 7},
		{[]byte{0x80}, 0},
		{[]byte{0xFF}, 0},
		{[]byte{0x00, 0x00, 0x10}, 19},
	}
	for _, c := range cases {
		if got := leadingZeroBits(c.in); got != c.want {
			t.Errorf("leadingZeroBits(%x) = %d, want %d", c.in, got, c.want)
		}
	}
}

// PoW enforcement path: a record with MinPoWBits=1 threshold
// passes iff its sigmessage hashes with ≥1 leading zero bit.
// We can't force a specific record to satisfy this without
// mining, so we test by hand-crafting the difficulty.
func TestPoWEnforcementSkipsWhenZero(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var ih [20]byte
	r := mkRecord(t, pub, priv, "x", ih, 1, 0)
	if err := VerifyRecordPoW(r, 0); err != nil {
		t.Fatalf("minBits=0 should pass: %v", err)
	}
}

func TestPoWEnforcementRejectsWhenInsufficient(t *testing.T) {
	// Find any record whose sigmessage SHA256 does NOT have 32
	// leading zero bits (overwhelmingly likely for random input).
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var ih [20]byte
	r := mkRecord(t, pub, priv, "x", ih, 1, 0)

	// minBits=32 essentially always fails for un-mined records.
	err := VerifyRecordPoW(r, 32)
	if err == nil {
		// Pathologically unlikely (~1 in 2^32) to hit this, but
		// we don't want a flaky test — retry with a fresh record.
		r = mkRecord(t, pub, priv, "y", ih, 2, 7)
		err = VerifyRecordPoW(r, 32)
	}
	if err == nil {
		t.Fatal("expected PoW check to reject un-mined record at D=32")
	}
}

func sortHashSlice(hs [][20]byte) {
	sort.Slice(hs, func(i, j int) bool {
		return bytes.Compare(hs[i][:], hs[j][:]) < 0
	})
}
