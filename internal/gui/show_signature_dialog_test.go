package gui

import (
	"testing"

	"github.com/swartznet/swartznet/internal/engine"
)

// TestShowSignatureDialogUnsignedShortCircuits covers the
// `if snap.SignedBy == "" { return }` early-return at
// downloads.go:352-354. Without a publisher pubkey there is
// nothing to render, so the function must exit before touching
// dl.d (a daemon-less downloadsTab is sufficient).
func TestShowSignatureDialogUnsignedShortCircuits(t *testing.T) {
	t.Parallel()
	dl := &downloadsTab{}
	dl.showSignatureDialog(engine.TorrentSnapshot{
		Name:     "unsigned torrent",
		InfoHash: "deadbeef",
		// SignedBy intentionally empty.
	})
}
