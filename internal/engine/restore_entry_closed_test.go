package engine

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
)

// TestRestoreEntryAfterCloseFails covers restoreEntry's
// `if e.closed { return error }` early-return arm. Close the
// engine, then call restoreEntry directly (we're inside the
// package so e.closed is reachable). Must surface
// "engine: closed" rather than crash on a half-initialized
// torrent client.
func TestRestoreEntryAfterCloseFails(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true
	cfg.IdentityPath = ""
	cfg.ReputationPath = ""
	cfg.SeedListPath = ""
	cfg.BloomPath = ""
	cfg.TrustPath = ""

	eng, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Engine is now closed. restoreEntry must return "closed"
	// on the very first thing it does, before touching any
	// torrent.Client state.
	err = eng.restoreEntry(sessionEntry{
		InfoHash:  "0000000000000000000000000000000000000000",
		AddedVia:  "magnet",
		MagnetURI: "magnet:?xt=urn:btih:0000000000000000000000000000000000000000",
	})
	if err == nil {
		t.Error("restoreEntry on closed engine should error")
	}
}
