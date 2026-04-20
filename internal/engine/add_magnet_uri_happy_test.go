package engine_test

import (
	"crypto/rand"
	"encoding/hex"
	"testing"
)

// TestAddMagnetURIHappyPath covers the happy-path return of
// AddMagnetURI — the existing tests only exercise the
// error branches (malformed URI, empty string). Add a magnet
// with a random infohash and verify the returned hex round-trips.
func TestAddMagnetURIHappyPath(t *testing.T) {
	t.Parallel()
	eng := newTestEngine(t)

	var ih [20]byte
	if _, err := rand.Read(ih[:]); err != nil {
		t.Fatal(err)
	}
	want := hex.EncodeToString(ih[:])
	uri := "magnet:?xt=urn:btih:" + want

	got, err := eng.AddMagnetURI(uri)
	if err != nil {
		t.Fatalf("AddMagnetURI: %v", err)
	}
	if got != want {
		t.Errorf("infohash = %q, want %q", got, want)
	}
}
