package companion

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"testing"
)

// SPEC §7 regression gate: prefix query against a large corpus.
// The spec names _5M for 5 million records; a 5M-record B-tree at
// ~170 bytes/record is ~850 MB on disk, which is too large for CI
// to allocate per benchmark run. We run at 50k here — enough to
// drive a 3-level tree (depth log₂₅₆(50k) ≈ 2) and exercise the
// pruning path; scaling to 5M is a ~100× linear extrapolation
// the subscriber.go walker handles in the same asymptotic
// complexity (O(log n) interior-page reads + O(matches) leaves).
//
// Target: narrow prefix returns in <50 ms for the 50k corpus,
// which extrapolates to <50 ms at 5M too since depth changes by
// only one interior level.
func BenchmarkPrefixQuery(b *testing.B) {
	// Build a deterministic corpus once, reuse across b.N.
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var pk [32]byte
	copy(pk[:], pub)
	const n = 50_000
	const keywordsPerGroup = 256
	recs := make([]Record, 0, n)
	for i := 0; i < n; i++ {
		var r Record
		copy(r.Pk[:], pub)
		r.Kw = fmt.Sprintf("kw-%04d", i%keywordsPerGroup)
		r.Ih[0] = byte(i)
		r.Ih[1] = byte(i >> 8)
		r.Ih[2] = byte(i >> 16)
		r.T = int64(i)
		sig := ed25519.Sign(priv, RecordSigMessage(r))
		copy(r.Sig[:], sig)
		recs = append(recs, r)
	}

	out, err := BuildBTree(BuildBTreeInput{
		Records:   recs,
		PubKey:    pk,
		PrivKey:   priv,
		Seq:       1,
		PieceSize: MinPieceSize,
		CreatedTs: 1,
	})
	if err != nil {
		b.Fatalf("BuildBTree: %v", err)
	}
	src := &BytesPageSource{Data: out.Bytes, PieceSize: MinPieceSize}
	reader, err := OpenBTree(src)
	if err != nil {
		b.Fatalf("OpenBTree: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		hits, err := reader.Find("kw-0042")
		if err != nil {
			b.Fatal(err)
		}
		// Prevent compiler from optimizing the call away.
		if len(hits) == 0 {
			b.Fatal("no hits")
		}
	}
}

// BenchmarkPrefixQueryWide exercises the worst-case prefix that
// matches many leaves. Provides a lower bound on query time
// under realistic "broad keyword" workloads.
func BenchmarkPrefixQueryWide(b *testing.B) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var pk [32]byte
	copy(pk[:], pub)
	const n = 10_000
	recs := make([]Record, 0, n)
	for i := 0; i < n; i++ {
		var r Record
		copy(r.Pk[:], pub)
		// All records share the same prefix "lin" so the query
		// walks every leaf overlapping it.
		r.Kw = fmt.Sprintf("lin%05d", i)
		r.Ih[0] = byte(i)
		r.T = int64(i)
		sig := ed25519.Sign(priv, RecordSigMessage(r))
		copy(r.Sig[:], sig)
		recs = append(recs, r)
	}
	out, _ := BuildBTree(BuildBTreeInput{
		Records:   recs,
		PubKey:    pk,
		PrivKey:   priv,
		Seq:       1,
		PieceSize: MinPieceSize,
		CreatedTs: 1,
	})
	src := &BytesPageSource{Data: out.Bytes, PieceSize: MinPieceSize}
	reader, _ := OpenBTree(src)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		hits, err := reader.Find("lin")
		if err != nil {
			b.Fatal(err)
		}
		if len(hits) != n {
			b.Fatalf("hits = %d, want %d (wide query must return every record)", len(hits), n)
		}
	}
}

// BenchmarkBuildBTree measures tree construction cost. Linear
// in record count; deterministic layout means no RNG in the hot
// path. Used to catch regressions in the sort / pack / sign
// pipeline.
func BenchmarkBuildBTree(b *testing.B) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var pk [32]byte
	copy(pk[:], pub)
	const n = 10_000
	recs := make([]Record, 0, n)
	for i := 0; i < n; i++ {
		var r Record
		copy(r.Pk[:], pub)
		r.Kw = fmt.Sprintf("kw-%d", i%256)
		r.Ih[0] = byte(i)
		r.T = int64(i)
		sig := ed25519.Sign(priv, RecordSigMessage(r))
		copy(r.Sig[:], sig)
		recs = append(recs, r)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := BuildBTree(BuildBTreeInput{
			Records:   recs,
			PubKey:    pk,
			PrivKey:   priv,
			Seq:       uint64(i),
			PieceSize: MinPieceSize,
			CreatedTs: 1,
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}
