package engine_test

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"
)

// TestAddMagnetAfterCloseErrors covers the
// `e.closed → return "engine: closed"` defensive branch of
// AddMagnet. Close the engine, then attempt an AddMagnet — the
// guard must return the closed-engine error rather than letting
// the call fall through to the (now-shutdown) anacrolix client.
func TestAddMagnetAfterCloseErrors(t *testing.T) {
	t.Parallel()
	eng, cleanup := newAddTorrentFileEngine(t)
	cleanup() // close immediately

	var ih [20]byte
	if _, err := rand.Read(ih[:]); err != nil {
		t.Fatal(err)
	}
	uri := "magnet:?xt=urn:btih:" + hex.EncodeToString(ih[:])
	_, err := eng.AddMagnet(uri)
	if err == nil {
		t.Error("AddMagnet on a closed engine should error")
	}
	if !strings.Contains(err.Error(), "closed") {
		t.Errorf("err = %q, want it to mention 'closed'", err.Error())
	}
}
