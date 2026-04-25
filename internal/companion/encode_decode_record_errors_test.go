package companion

import (
	"strings"
	"testing"

	"github.com/anacrolix/torrent/bencode"
)

// TestEncodeRecordEmptyKeywordRejected — EncodeRecord must
// reject a record whose Kw is empty before bencode marshaling
// runs, preserving the invariant that every leaf has a usable
// sort key.
func TestEncodeRecordEmptyKeywordRejected(t *testing.T) {
	t.Parallel()
	var r Record
	r.Pk[0] = 0xAA
	r.Ih[0] = 0xBB
	// r.Kw is empty
	if _, err := EncodeRecord(r); err == nil {
		t.Error("EncodeRecord with empty Kw should error")
	}
}

// TestDecodeRecordRejectsMalformedBencode — random bytes that
// don't parse as bencode bubble through unmarshal as an error.
func TestDecodeRecordRejectsMalformedBencode(t *testing.T) {
	t.Parallel()
	if _, err := DecodeRecord([]byte("garbage-not-bencode")); err == nil {
		t.Error("DecodeRecord should reject garbage")
	}
}

// TestDecodeRecordRejectsBadFieldLengths — exercises every
// length-validation branch in DecodeRecord. We synthesise the
// wire form by direct bencode marshaling so we can produce
// records that look syntactically valid but carry wrong-sized
// pk/ih/sig fields.
func TestDecodeRecordRejectsBadFieldLengths(t *testing.T) {
	t.Parallel()
	mk := func(pk, ih, sig []byte, kw string) []byte {
		w := recordWire{Pk: pk, Kw: kw, Ih: ih, T: 1, Pow: 0, Sig: sig}
		out, err := bencode.Marshal(w)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return out
	}
	good32 := make([]byte, 32)
	good20 := make([]byte, 20)
	good64 := make([]byte, 64)

	cases := []struct {
		name string
		raw  []byte
	}{
		{"short pk", mk(make([]byte, 16), good20, good64, "k")},
		{"long pk", mk(make([]byte, 64), good20, good64, "k")},
		{"short ih", mk(good32, make([]byte, 10), good64, "k")},
		{"long ih", mk(good32, make([]byte, 40), good64, "k")},
		{"short sig", mk(good32, good20, make([]byte, 32), "k")},
		{"long sig", mk(good32, good20, make([]byte, 128), "k")},
		{"oversize kw", mk(good32, good20, good64, strings.Repeat("x", MaxKeywordBytes+1))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeRecord(tc.raw); err == nil {
				t.Error("expected error")
			}
		})
	}
}
