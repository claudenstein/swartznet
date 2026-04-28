package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
)

// TestBuildAndSignWrongIHByteLen covers buildAndSign's
// `if len(ihBytes) != 20` defensive guard. readRecords gates
// jr.IH at 40 chars in production, but a direct caller could
// pass a hex-valid string of different length — that's the
// case this test exercises (38 hex chars → 19 bytes → trip).
func TestBuildAndSignWrongIHByteLen(t *testing.T) {
	t.Parallel()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var pk [32]byte
	copy(pk[:], pub)

	recs := []jsonRecord{
		// 38 hex chars decodes to 19 bytes — passes hex.DecodeString
		// but trips the != 20 byte-length guard.
		{IH: strings.Repeat("ab", 19), Kw: "hello", T: 1},
	}
	_, err = buildAndSign(recs, pub, priv, pk, 0, 16384, 0)
	if err == nil {
		t.Fatal("expected error from 19-byte ih, got nil")
	}
	if !strings.Contains(err.Error(), "ih decoded to 19 bytes") {
		t.Errorf("unexpected error %q, want to mention 19 bytes", err)
	}
}
