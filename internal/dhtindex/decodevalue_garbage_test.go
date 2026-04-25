package dhtindex_test

import (
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// TestDecodeValueGarbageBencode covers the bencode.Unmarshal
// error branch of DecodeValue — payload has nonzero length but
// is not valid bencode, so unmarshal fails and the wrapped
// "decode value" error must propagate.
func TestDecodeValueGarbageBencode(t *testing.T) {
	t.Parallel()
	cases := [][]byte{
		[]byte("not bencode"),
		{0xFF, 0xFE, 0xFD},  // raw garbage bytes
		[]byte("d3:tsi42e"), // truncated dict — no closing 'e'
	}
	for _, payload := range cases {
		_, err := dhtindex.DecodeValue(payload)
		if err == nil {
			t.Errorf("DecodeValue on %q should error", payload)
			continue
		}
		if !strings.Contains(err.Error(), "decode value") {
			t.Errorf("err = %q, want it to wrap 'decode value'", err.Error())
		}
	}
}
