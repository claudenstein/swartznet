package dhtindex

import (
	"bytes"
	"crypto/ed25519"
	"testing"

	"github.com/swartznet/swartznet/contracts/snagg"
)

// buildTestTree builds a tiny signed SNAGG tree and returns (bytes, fingerprint).
func buildTestTree(t *testing.T) ([]byte, [32]byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	var pubArr [32]byte
	copy(pubArr[:], pub)
	mkRec := func(kw string, ihSeed byte) snagg.Record {
		var r snagg.Record
		r.Pk = pubArr
		r.Kw = kw
		for i := range r.Ih {
			r.Ih[i] = ihSeed + byte(i)
		}
		r.T = 1700000000
		copy(r.Sig[:], ed25519.Sign(priv, r.SigMessage()))
		return r
	}
	built, err := snagg.BuildBTree(snagg.BuildInput{
		Records:   []snagg.Record{mkRec("ubuntu", 0x10), mkRec("debian", 0x20)},
		PubKey:    pubArr,
		PrivKey:   priv,
		Seq:       1,
		CreatedTs: 1700000000,
		PieceSize: snagg.MinPieceSize,
	})
	if err != nil {
		t.Fatal(err)
	}
	return built.Bytes, built.Fingerprint
}

func TestWrapSnaggTorrentPieceLengthAligns(t *testing.T) {
	t.Parallel()
	data, _ := buildTestTree(t)
	mi, err := WrapSnaggTorrent("swartznet-aggregate-abc.snagg", data)
	if err != nil {
		t.Fatal(err)
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatal(err)
	}
	if info.PieceLength != SnaggPieceLength {
		t.Errorf("piece length = %d, want %d (must align with SNAGG pages)", info.PieceLength, SnaggPieceLength)
	}
	if info.Length != int64(len(data)) {
		t.Errorf("length = %d, want %d", info.Length, len(data))
	}
	// Deterministic: the same tree bytes wrap to the same infohash.
	mi2, _ := WrapSnaggTorrent("swartznet-aggregate-abc.snagg", data)
	if mi.HashInfoBytes() != mi2.HashInfoBytes() {
		t.Error("wrapping is not deterministic")
	}
}

func TestWrapSnaggTorrentRejectsMisaligned(t *testing.T) {
	t.Parallel()
	if _, err := WrapSnaggTorrent("x", make([]byte, SnaggPieceLength+1)); err == nil {
		t.Error("non-piece-multiple payload accepted")
	}
	if _, err := WrapSnaggTorrent("x", nil); err == nil {
		t.Error("empty payload accepted")
	}
}

func TestOpenVerifiedTreeCommitBinding(t *testing.T) {
	t.Parallel()
	data, fp := buildTestTree(t)
	// Matching commit → opens + queries.
	tree, err := OpenVerifiedTree(data, fp[:])
	if err != nil {
		t.Fatalf("open with matching commit: %v", err)
	}
	hits, err := tree.Find("ubuntu")
	if err != nil || len(hits) != 1 {
		t.Errorf("Find(ubuntu) = %d hits, err %v", len(hits), err)
	}
	// Wrong commit → rejected (the pointer→tree binding).
	wrong := bytes.Repeat([]byte{0xff}, 32)
	if _, err := OpenVerifiedTree(data, wrong); err == nil {
		t.Error("tree with mismatched commit accepted")
	}
	// Empty commit → trailer signature still gates, opens fine.
	if _, err := OpenVerifiedTree(data, nil); err != nil {
		t.Errorf("open with no commit binding: %v", err)
	}
	// Corrupt a trailer SIGNATURE byte → OpenBTree rejects regardless of commit.
	// The trailer is the last page; its signature is at payload offset 98..162
	// (page bytes header+98 = 16+98).
	bad := append([]byte(nil), data...)
	trailerOff := len(bad) - SnaggPieceLength
	bad[trailerOff+snagg.PageHeaderSize+120] ^= 0xFF // a byte inside the 64-byte sig
	if _, err := OpenVerifiedTree(bad, fp[:]); err == nil {
		t.Error("corrupt trailer signature accepted")
	}
}
