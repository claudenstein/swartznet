package dhtindex_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

func TestTokenizeBasic(t *testing.T) {
	t.Parallel()
	got := dhtindex.SortedTokenize("Ubuntu 24.04 Desktop amd64")
	// "24" and "04" are 2-byte tokens, dropped by MinTokenBytes=3.
	// "amd64" survives because it's a single multi-byte token.
	want := []string{"amd64", "desktop", "ubuntu"}
	if !slices.Equal(got, want) {
		t.Errorf("Tokenize = %v, want %v", got, want)
	}
}

func TestTokenizeDropsStopwords(t *testing.T) {
	t.Parallel()
	got := dhtindex.SortedTokenize("the quick brown fox")
	// "the" is dropped; "fox", "brown", "quick" remain.
	want := []string{"brown", "fox", "quick"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestTokenizeDropsExtensions(t *testing.T) {
	t.Parallel()
	got := dhtindex.SortedTokenize("ubuntu-24.04.iso")
	// "iso" is in extensionTokens; "24" and "04" too short; only
	// "ubuntu" remains.
	want := []string{"ubuntu"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestTokenizeDropsShortTokens(t *testing.T) {
	t.Parallel()
	// "a", "i", "ad" are too short; "ben", "linux" survive.
	got := dhtindex.SortedTokenize("a i ad ben linux")
	want := []string{"ben", "linux"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestTokenizeUnicodeLetters(t *testing.T) {
	t.Parallel()
	// Non-ASCII letters should still tokenise.
	got := dhtindex.Tokenize("Über die Brücke")
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	for _, tok := range []string{"über", "die", "brücke"} {
		if !slices.Contains(got, tok) {
			// "die" is short and German for "the" — but our
			// stopwords list is English-only, so it stays.
			t.Errorf("missing %q in %v", tok, got)
		}
	}
}

func TestTokenizeDeduplication(t *testing.T) {
	t.Parallel()
	// Repeated words become one entry.
	got := dhtindex.Tokenize("ubuntu ubuntu UBUNTU Ubuntu")
	if len(got) != 1 || got[0] != "ubuntu" {
		t.Errorf("got %v, want [ubuntu]", got)
	}
}

func TestTokenizeRespectsMaxKeywords(t *testing.T) {
	t.Parallel()
	name := strings.Repeat("alpha beta gamma delta epsilon zeta eta theta iota kappa ", 1)
	got := dhtindex.Tokenize(name)
	if len(got) > dhtindex.MaxKeywordsPerTorrent {
		t.Errorf("len = %d, want <= %d", len(got), dhtindex.MaxKeywordsPerTorrent)
	}
}

func TestTokenizeAllReturnsAll(t *testing.T) {
	t.Parallel()
	// All 12 words are 3+ bytes so the MinTokenBytes filter doesn't
	// drop any of them. (Earlier draft used "mu" — 2 bytes — which
	// would be dropped.)
	name := "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda omega"
	all := dhtindex.TokenizeAll(name)
	if len(all) != 12 {
		t.Errorf("TokenizeAll len = %d, want 12 (no cap)", len(all))
	}
}

func TestTokenizeEmpty(t *testing.T) {
	t.Parallel()
	if got := dhtindex.Tokenize(""); got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestTokenizeMixedSeparators(t *testing.T) {
	t.Parallel()
	got := dhtindex.SortedTokenize("Linux_kernel-6.10.0[stable](amd64)")
	want := []string{"amd64", "kernel", "linux", "stable"}
	// "10" and "6" are dropped (too short → 1/2 bytes; "10" is 2 bytes).
	// Wait — "10" is 2 bytes, MinTokenBytes is 3 → dropped. "6" → 1 byte → dropped.
	// "0" → 1 byte → dropped.
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("missing %q in %v", w, got)
		}
	}
}
