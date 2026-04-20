package dhtindex

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
)

// TestPublishOneUnknownKeywordEarlyReturn covers the
// `!ok → return` branch of publishOne — calling publishOne for
// a keyword that's not in the manifest must early-return
// without firing a Put. refreshAll's loop iterates over snap
// keys so this branch is unreachable from production, but the
// guard exists for defensive correctness.
func TestPublishOneUnknownKeywordEarlyReturn(t *testing.T) {
	t.Parallel()
	mf, _ := LoadOrCreateManifest("")
	put := &recordingPutter{}
	p := NewPublisher(put, mf, PublisherOptions{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Manifest is empty; call publishOne anyway.
	p.publishOne(context.Background(), "no-such-keyword")

	if got := put.calls.Load(); got != 0 {
		t.Errorf("Put calls = %d, want 0 for missing keyword", got)
	}
}

// TestPublishOneSaltErrorMarksFailed covers the
// `SaltForKeyword(keyword) err → MarkFailed + return` branch.
// Add a hit under an oversized keyword (> MaxSaltBytes), then
// call publishOne; the salt computation must fail and the
// entry's LastError must reflect it.
func TestPublishOneSaltErrorMarksFailed(t *testing.T) {
	t.Parallel()
	mf, _ := LoadOrCreateManifest("")
	tooLong := strings.Repeat("x", MaxSaltBytes+1)
	if _, err := mf.AddHit(tooLong, KeywordHit{IH: bytes.Repeat([]byte{1}, 20), N: "u"}); err != nil {
		t.Fatal(err)
	}

	put := &recordingPutter{}
	p := NewPublisher(put, mf, PublisherOptions{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	p.publishOne(context.Background(), tooLong)

	if got := put.calls.Load(); got != 0 {
		t.Errorf("Put calls = %d, want 0 (salt failure should skip Put)", got)
	}
	entry := mf.Entries[tooLong]
	if entry == nil {
		t.Fatal("manifest entry missing")
	}
	if entry.LastError == "" {
		t.Errorf("LastError empty; want a salt-error message")
	}
}
