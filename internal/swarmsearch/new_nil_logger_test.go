package swarmsearch_test

import (
	"testing"

	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// TestNewNilLoggerFallsBackToDefault covers the `log == nil`
// branch in New — the constructor must substitute slog.Default()
// rather than store nil and panic on the first log call.
func TestNewNilLoggerFallsBackToDefault(t *testing.T) {
	t.Parallel()
	p := swarmsearch.New(nil)
	if p == nil {
		t.Fatal("New(nil) returned nil")
	}
	// Trigger a log path to confirm the substituted logger works.
	// HandleMessage on garbage payload calls chargeMisbehavior,
	// which logs via p.log — would panic if log were still nil.
	p.HandleMessage("1.2.3.4:6881", []byte("not bencode"), nil)
}
