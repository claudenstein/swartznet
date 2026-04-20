package dhtindex_test

import (
	"errors"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// TestMarkPublishedUnknownKeywordNoop covers the !ok || entry==nil
// guard branch in MarkPublished. The existing tests only call
// MarkPublished after AddHit, so the missing-keyword case stays
// uncovered.
func TestMarkPublishedUnknownKeywordNoop(t *testing.T) {
	t.Parallel()
	mf, _ := dhtindex.LoadOrCreateManifest("")

	// No AddHit first — the keyword is unknown.
	mf.MarkPublished("never-added", time.Now())

	if got := len(mf.Snapshot()); got != 0 {
		t.Errorf("snapshot should be empty after no-op MarkPublished, got %d entries", got)
	}
}

func TestMarkFailedUnknownKeywordNoop(t *testing.T) {
	t.Parallel()
	mf, _ := dhtindex.LoadOrCreateManifest("")

	mf.MarkFailed("never-added", errors.New("nope"))

	if got := len(mf.Snapshot()); got != 0 {
		t.Errorf("snapshot should be empty after no-op MarkFailed, got %d entries", got)
	}
}

// TestMarkFailedNilErrorDoesNotOverwriteLastError covers the
// guard inside MarkFailed: if err is nil, LastError is left
// untouched (so a transient nil call doesn't accidentally clear a
// real prior failure record).
func TestMarkFailedNilErrorDoesNotOverwriteLastError(t *testing.T) {
	t.Parallel()
	mf, _ := dhtindex.LoadOrCreateManifest("")
	if _, err := mf.AddHit("ubuntu", dhtindex.KeywordHit{IH: ihBytes(1), N: "x"}); err != nil {
		t.Fatal(err)
	}
	// Set a real LastError first.
	mf.MarkFailed("ubuntu", errors.New("dht timeout"))
	if got := mf.Snapshot()["ubuntu"].LastError; got != "dht timeout" {
		t.Fatalf("LastError = %q, want \"dht timeout\"", got)
	}

	// Now call MarkFailed with nil — must NOT clear the prior error.
	mf.MarkFailed("ubuntu", nil)
	if got := mf.Snapshot()["ubuntu"].LastError; got != "dht timeout" {
		t.Errorf("LastError = %q after nil MarkFailed, want unchanged \"dht timeout\"", got)
	}
}
