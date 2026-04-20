package signing_test

import (
	"strings"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/swartznet/swartznet/internal/signing"
)

// TestVerifyBytesBadPubkeyDecode covers the
// `bencode.Unmarshal(pubRaw, &pub) err → wrapped "decode pubkey"`
// branch of VerifyBytes. Hand-build a metainfo dict whose
// snet.pubkey field is a non-string bencode value (an int)
// so the secondary Unmarshal fails.
func TestVerifyBytesBadPubkeyDecode(t *testing.T) {
	t.Parallel()
	info := miniTorrent(t)
	// Start from a valid minimal signed-shape: info + snet.pubkey + snet.sig.
	var mi map[string]bencode.Bytes
	if err := bencode.Unmarshal(info, &mi); err != nil {
		t.Fatal(err)
	}
	// snet.pubkey is an integer, not a string — will fail the
	// secondary bencode.Unmarshal to string.
	intBytes, err := bencode.Marshal(42)
	if err != nil {
		t.Fatal(err)
	}
	mi["snet.pubkey"] = intBytes
	// Any bytes for snet.sig so the ErrNotSigned short-circuit
	// doesn't fire first.
	sigBytes, err := bencode.Marshal("not-a-sig")
	if err != nil {
		t.Fatal(err)
	}
	mi["snet.sig"] = sigBytes

	raw, err := bencode.Marshal(mi)
	if err != nil {
		t.Fatal(err)
	}

	_, verr := signing.VerifyBytes(raw)
	if verr == nil {
		t.Fatal("VerifyBytes should fail when snet.pubkey is not a string")
	}
	if !strings.Contains(verr.Error(), "decode pubkey") {
		t.Errorf("err = %q, want it to wrap 'decode pubkey'", verr.Error())
	}
}

// TestVerifyBytesBadSigDecode covers the sibling branch for
// snet.sig.
func TestVerifyBytesBadSigDecode(t *testing.T) {
	t.Parallel()
	info := miniTorrent(t)
	var mi map[string]bencode.Bytes
	if err := bencode.Unmarshal(info, &mi); err != nil {
		t.Fatal(err)
	}
	pubBytes, err := bencode.Marshal(strings.Repeat("p", 32))
	if err != nil {
		t.Fatal(err)
	}
	mi["snet.pubkey"] = pubBytes
	// snet.sig is an int — will fail the string Unmarshal.
	intBytes, err := bencode.Marshal(99)
	if err != nil {
		t.Fatal(err)
	}
	mi["snet.sig"] = intBytes

	raw, err := bencode.Marshal(mi)
	if err != nil {
		t.Fatal(err)
	}

	_, verr := signing.VerifyBytes(raw)
	if verr == nil {
		t.Fatal("VerifyBytes should fail when snet.sig is not a string")
	}
	if !strings.Contains(verr.Error(), "decode sig") {
		t.Errorf("err = %q, want it to wrap 'decode sig'", verr.Error())
	}
}
