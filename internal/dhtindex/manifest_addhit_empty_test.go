package dhtindex_test

import (
	"testing"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// TestManifestAddHitEmptyKeyword covers AddHit's
// `if keyword == "" { return 0, errors.New("...") }` guard.
func TestManifestAddHitEmptyKeyword(t *testing.T) {
	t.Parallel()
	m, err := dhtindex.LoadOrCreateManifest("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddHit("", dhtindex.KeywordHit{IH: ihBytes(1), N: "x"}); err == nil {
		t.Error("AddHit with empty keyword should error")
	}
}
