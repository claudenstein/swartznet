package swarmsearch_test

import (
	"bytes"
	"testing"

	"github.com/swartznet/swartznet/internal/swarmsearch"
)

func TestEncodeDecodeQueryRoundTrip(t *testing.T) {
	t.Parallel()
	orig := swarmsearch.Query{
		TxID:    42,
		Q:       "ubuntu 24.04",
		Scope:   "nfc",
		Limit:   50,
		Lang:    "en",
		MinSize: 1024,
		MaxSize: 10 * 1024 * 1024 * 1024,
		NotIH: [][]byte{
			bytes.Repeat([]byte{0xab}, 20),
			bytes.Repeat([]byte{0xcd}, 20),
		},
	}
	payload, err := swarmsearch.EncodeQuery(orig)
	if err != nil {
		t.Fatalf("EncodeQuery: %v", err)
	}
	got, err := swarmsearch.DecodeQuery(payload)
	if err != nil {
		t.Fatalf("DecodeQuery: %v", err)
	}
	if got.TxID != orig.TxID || got.Q != orig.Q || got.Scope != orig.Scope ||
		got.Limit != orig.Limit || got.Lang != orig.Lang ||
		got.MinSize != orig.MinSize || got.MaxSize != orig.MaxSize {
		t.Errorf("round-trip mismatch\nwant %+v\ngot  %+v", orig, got)
	}
	if len(got.NotIH) != 2 || !bytes.Equal(got.NotIH[0], orig.NotIH[0]) || !bytes.Equal(got.NotIH[1], orig.NotIH[1]) {
		t.Errorf("NotIH round-trip failed: %v", got.NotIH)
	}
	if got.MsgType != swarmsearch.MsgTypeQuery {
		t.Errorf("MsgType = %d, want %d", got.MsgType, swarmsearch.MsgTypeQuery)
	}
}

func TestEncodeDecodeResultRoundTrip(t *testing.T) {
	t.Parallel()
	orig := swarmsearch.Result{
		TxID:  1234,
		Total: 42,
		Hits: []swarmsearch.Hit{
			{
				IH:   bytes.Repeat([]byte{0x01}, 20),
				N:    "ubuntu 24.04 amd64 iso",
				S:    100,
				L:    50,
				Sz:   6 * 1024 * 1024 * 1024,
				T:    1712649600,
				Rank: 870,
			},
			{
				IH: bytes.Repeat([]byte{0x02}, 20),
				N:  "ubuntu server minimal",
				Matches: []swarmsearch.FileMatch{
					{FI: 3, FP: "README.md"},
					{FI: 4, FP: "docs/install.txt"},
				},
			},
		},
	}
	payload, err := swarmsearch.EncodeResult(orig)
	if err != nil {
		t.Fatalf("EncodeResult: %v", err)
	}
	got, err := swarmsearch.DecodeResult(payload)
	if err != nil {
		t.Fatalf("DecodeResult: %v", err)
	}
	if got.TxID != 1234 || got.Total != 42 {
		t.Errorf("scalar fields wrong: %+v", got)
	}
	if len(got.Hits) != 2 {
		t.Fatalf("got %d hits, want 2", len(got.Hits))
	}
	if got.Hits[0].N != "ubuntu 24.04 amd64 iso" || got.Hits[0].S != 100 || got.Hits[0].Rank != 870 {
		t.Errorf("first hit round-trip failed: %+v", got.Hits[0])
	}
	if len(got.Hits[1].Matches) != 2 {
		t.Errorf("matches len = %d, want 2", len(got.Hits[1].Matches))
	}
	if got.Hits[1].Matches[0].FP != "README.md" {
		t.Errorf("matches[0].FP = %q, want README.md", got.Hits[1].Matches[0].FP)
	}
}

func TestEncodeDecodeRejectRoundTrip(t *testing.T) {
	t.Parallel()
	orig := swarmsearch.Reject{
		TxID:   99,
		Code:   swarmsearch.RejectRateLimited,
		Reason: "over quota",
	}
	payload, err := swarmsearch.EncodeReject(orig)
	if err != nil {
		t.Fatal(err)
	}
	got, err := swarmsearch.DecodeReject(payload)
	if err != nil {
		t.Fatal(err)
	}
	if got.TxID != 99 || got.Code != swarmsearch.RejectRateLimited || got.Reason != "over quota" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestDecodeWrongTypeRejects(t *testing.T) {
	t.Parallel()
	// Encode a query, try to decode as a result → must error.
	payload, err := swarmsearch.EncodeQuery(swarmsearch.Query{TxID: 1, Q: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := swarmsearch.DecodeResult(payload); err == nil {
		t.Error("decoded a query as a result without error")
	}
	if _, err := swarmsearch.DecodeReject(payload); err == nil {
		t.Error("decoded a query as a reject without error")
	}
}

func TestDecodeGarbageRejects(t *testing.T) {
	t.Parallel()
	if _, err := swarmsearch.DecodeQuery([]byte("not bencode at all")); err == nil {
		t.Error("decoded garbage without error")
	}
}
