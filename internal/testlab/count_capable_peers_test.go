package testlab

import (
	"io"
	"log/slog"
	"testing"

	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// TestCountCapablePeersNilSafe exercises the documented nil-
// protocol guard. Production never passes nil here, but the
// defensive check exists so test scaffolding can call the
// helper before the cluster is fully wired up.
func TestCountCapablePeersNilSafe(t *testing.T) {
	t.Parallel()
	if got := countCapablePeers(nil); got != 0 {
		t.Errorf("countCapablePeers(nil) = %d, want 0", got)
	}
}

// TestCountCapablePeersEmptyProtocol covers the non-nil branch
// against a fresh Protocol with zero peers — the helper must
// return 0 without consulting any peer state.
func TestCountCapablePeersEmptyProtocol(t *testing.T) {
	t.Parallel()
	p := swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if got := countCapablePeers(p); got != 0 {
		t.Errorf("countCapablePeers(empty protocol) = %d, want 0", got)
	}
}
