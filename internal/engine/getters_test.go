package engine_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
	"github.com/swartznet/swartznet/internal/indexer"
)

// newGettersEngine spins up a DHT-disabled, no-upload engine for the
// getter tests. Returns the engine and a cleanup callback.
func newGettersEngine(t *testing.T) (*engine.Engine, func()) {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true
	// Clear the file-backed paths so the engine starts with a
	// fully-empty side-state set; the DHT-bound and file-backed
	// getters all return nil for a deterministic assertion.
	cfg.IdentityPath = ""
	cfg.ReputationPath = ""
	cfg.SeedListPath = ""
	cfg.BloomPath = ""
	cfg.TrustPath = ""

	eng, err := engine.New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	return eng, func() { _ = eng.Close() }
}

// TestEngineGettersDHTDisabled exercises the simple accessor methods
// on an engine with no DHT, no identity path, no bloom/reputation/
// trust paths. The DHT-bound accessors must return nil; the
// always-on collaborators (SwarmSearch, SourceTracker) must not.
func TestEngineGettersDHTDisabled(t *testing.T) {
	t.Parallel()
	eng, cleanup := newGettersEngine(t)
	defer cleanup()

	if eng.Publisher() != nil {
		t.Error("Publisher should be nil with DHT disabled")
	}
	if eng.PointerPutter() != nil {
		t.Error("PointerPutter should be nil with DHT disabled")
	}
	if eng.PointerGetter() != nil {
		t.Error("PointerGetter should be nil with DHT disabled")
	}
	if eng.Identity() != nil {
		t.Error("Identity should be nil with no IdentityPath")
	}
	if eng.Lookup() != nil {
		t.Error("Lookup should be nil with DHT disabled")
	}
	if eng.ReputationTracker() != nil {
		t.Error("ReputationTracker should be nil with no ReputationPath")
	}
	if eng.KnownGoodBloom() != nil {
		t.Error("KnownGoodBloom should be nil with no BloomPath")
	}
	if eng.TrustStore() != nil {
		t.Error("TrustStore should be nil with no TrustPath")
	}

	// Always-on collaborators.
	if eng.SwarmSearch() == nil {
		t.Error("SwarmSearch must be non-nil")
	}
	if eng.SourceTracker() == nil {
		t.Error("SourceTracker must be non-nil")
	}
}

// TestEngineSetIndexAttachAndDetach round-trips SetIndex twice: once
// with a real index (wires the pipeline + sn_search searcher), and
// once with nil (unwires both). The second call must not panic on
// the previously-attached pipeline.
func TestEngineSetIndexAttachAndDetach(t *testing.T) {
	t.Parallel()
	eng, cleanup := newGettersEngine(t)
	defer cleanup()

	idx, err := indexer.Open(filepath.Join(t.TempDir(), "idx"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	eng.SetIndex(idx)
	// Replace with nil — must stop the old pipeline cleanly.
	eng.SetIndex(nil)
	// Re-attach the same index to verify the cycle is repeatable.
	eng.SetIndex(idx)
}

func TestEngineAddMagnetURIBadInput(t *testing.T) {
	t.Parallel()
	eng, cleanup := newGettersEngine(t)
	defer cleanup()

	if _, err := eng.AddMagnetURI("not-a-magnet-uri"); err == nil {
		t.Error("AddMagnetURI with non-magnet URI should error")
	}
	// Empty input — anacrolix rejects this too.
	if _, err := eng.AddMagnetURI(""); err == nil {
		t.Error("AddMagnetURI with empty string should error")
	}
}

func TestEngineResumeTorrentUnknown(t *testing.T) {
	t.Parallel()
	eng, cleanup := newGettersEngine(t)
	defer cleanup()

	if err := eng.ResumeTorrent("0000000000000000000000000000000000000000"); err == nil {
		t.Error("ResumeTorrent on unknown infohash should error")
	}
}

func TestEngineHandleByInfoHashUnknown(t *testing.T) {
	t.Parallel()
	eng, cleanup := newGettersEngine(t)
	defer cleanup()

	var ih [20]byte
	ih[0] = 0xde
	if _, err := eng.HandleByInfoHash(ih); err == nil {
		t.Error("HandleByInfoHash on unknown should error")
	}
}

func TestEngineAddTrustedPeerEngineRejectsNilAndUnknown(t *testing.T) {
	t.Parallel()
	eng, cleanup := newGettersEngine(t)
	defer cleanup()

	var ih [20]byte
	ih[0] = 0xab

	if _, err := eng.AddTrustedPeerEngine(ih, nil); err == nil {
		t.Error("AddTrustedPeerEngine with nil other should error")
	}

	other, otherCleanup := newGettersEngine(t)
	defer otherCleanup()

	if _, err := eng.AddTrustedPeerEngine(ih, other); err == nil {
		t.Error("AddTrustedPeerEngine on unknown infohash should error")
	}
}

// TestEngineAddTrustedPeerEngineSuccess covers the success arm
// (`return h.T.AddClientPeer(other.client), nil`). Add the same
// magnet (zero-pieces, no metadata) to both engines so they
// share an infohash, then wire other into eng's peer set. The
// number of peer addresses returned reflects however many
// listen addresses anacrolix exposes, but it must be ≥0 and the
// call must not error.
func TestEngineAddTrustedPeerEngineSuccess(t *testing.T) {
	t.Parallel()
	eng, cleanup := newGettersEngine(t)
	defer cleanup()
	other, otherCleanup := newGettersEngine(t)
	defer otherCleanup()

	const magnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
	h1, err := eng.AddMagnet(magnet)
	if err != nil {
		t.Fatalf("eng.AddMagnet: %v", err)
	}
	_ = h1
	h2, err := other.AddMagnet(magnet)
	if err != nil {
		t.Fatalf("other.AddMagnet: %v", err)
	}
	_ = h2

	var ih [20]byte
	copy(ih[:], h1.T.InfoHash().Bytes())

	added, err := eng.AddTrustedPeerEngine(ih, other)
	if err != nil {
		t.Fatalf("AddTrustedPeerEngine: %v", err)
	}
	if added < 0 {
		t.Errorf("AddClientPeer returned negative count %d", added)
	}
}
