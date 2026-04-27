package dhtindex_test

import (
	"context"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// TestLookupQueryOversizedFirstToken covers Lookup.Query's
// `salt, err := SaltForKeyword(keyword); if err != nil { return … }`
// arm. Tokenize doesn't enforce a per-token byte cap, so a single
// 100-byte word survives tokenisation but trips SaltForKeyword's
// 64-byte BEP-44 cap.
func TestLookupQueryOversizedFirstToken(t *testing.T) {
	t.Parallel()
	l := dhtindex.NewLookup(nil)

	longKw := strings.Repeat("a", 100) // > MaxSaltBytes (64)
	if _, err := l.Query(context.Background(), longKw); err == nil {
		t.Error("Query should fail when first token exceeds the BEP-44 salt cap")
	}
}
