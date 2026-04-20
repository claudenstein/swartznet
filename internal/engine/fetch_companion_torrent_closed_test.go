package engine_test

import (
	"context"
	"crypto/rand"
	"strings"
	"testing"
)

// TestFetchCompanionTorrentEngineClosed covers the
// `AddInfoHash err → return "", err` branch of
// FetchCompanionTorrent. Close the engine first so AddInfoHash
// returns the closed-engine error, which FetchCompanionTorrent
// must propagate without trying to fetch metadata.
func TestFetchCompanionTorrentEngineClosed(t *testing.T) {
	t.Parallel()
	eng, cleanup := newAddTorrentFileEngine(t)
	cleanup() // close immediately

	var ih [20]byte
	if _, err := rand.Read(ih[:]); err != nil {
		t.Fatal(err)
	}
	_, err := eng.FetchCompanionTorrent(context.Background(), ih)
	if err == nil {
		t.Error("FetchCompanionTorrent on a closed engine should error")
	}
	if !strings.Contains(err.Error(), "closed") {
		t.Errorf("err = %q, want it to mention 'closed'", err.Error())
	}
}
