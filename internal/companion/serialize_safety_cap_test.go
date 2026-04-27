package companion_test

import (
	"bytes"
	"compress/gzip"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/companion"
)

// TestDecodeRejectsOversizeDecompressed covers Decode's
// `if int64(len(body)) > maxDecompressed → "exceeds 1 GiB safety
// cap"` arm at serialize.go:59-61. A small gzip-compressed stream
// of repeating bytes inflates to >1 GiB; the decoder reads up to
// (cap+1) via LimitReader and trips the safety guard.
//
// We cap maxDecompressed at 1 << 30 (1 GiB) in production, so we
// gzip a (1 << 30 + 1)-byte source. With "ababab…" content the
// gzip stream stays a few MB — manageable in CI.
func TestDecodeRejectsOversizeDecompressed(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("compresses 1 GiB; takes ~3s")
	}

	const overCap = (1 << 30) + 1
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	// 64 KiB of "ab" pattern, repeated until past 1 GiB.
	chunk := []byte(strings.Repeat("ab", 32*1024))
	written := 0
	for written < overCap {
		n := len(chunk)
		if written+n > overCap {
			n = overCap - written
		}
		if _, err := gz.Write(chunk[:n]); err != nil {
			t.Fatal(err)
		}
		written += n
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := companion.Decode(&buf); err == nil {
		t.Error("Decode should reject decompressed payload > 1 GiB")
	} else if !strings.Contains(err.Error(), "1 GiB") {
		t.Errorf("err = %q, want it to mention '1 GiB' cap", err.Error())
	}
}
