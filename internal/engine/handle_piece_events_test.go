package engine_test

import (
	"crypto/rand"
	"encoding/hex"
	"testing"
)

// TestHandlePieceEventsReturnsChannel covers the
// `Handle.PieceEvents()` accessor — currently 0% coverage
// because no test reaches it. Add a magnet, fetch the
// returned Handle, and assert PieceEvents returns a non-nil
// receive-only channel.
func TestHandlePieceEventsReturnsChannel(t *testing.T) {
	t.Parallel()
	eng, cleanup := newAddTorrentFileEngine(t)
	defer cleanup()

	var ih [20]byte
	if _, err := rand.Read(ih[:]); err != nil {
		t.Fatal(err)
	}
	uri := "magnet:?xt=urn:btih:" + hex.EncodeToString(ih[:])
	h, err := eng.AddMagnet(uri)
	if err != nil {
		t.Fatalf("AddMagnet: %v", err)
	}
	if h.PieceEvents() == nil {
		t.Errorf("PieceEvents() returned nil channel")
	}
}
