package token

import (
	"slices"
	"testing"
)

// tokenizeGolden freezes the Tokenize contract in-source (golden vectors, not
// generated artifacts). Outputs are in APPEARANCE order — that order is part
// of the cross-implementation contract.
var tokenizeGolden = []struct {
	name  string
	input string
	want  []string
}{
	{
		// "24" and "04" are 2-byte tokens, dropped by MinTokenBytes=3;
		// "amd64" survives as a single multi-byte token.
		name:  "basic with short digit groups",
		input: "Ubuntu 24.04 Desktop amd64",
		want:  []string{"ubuntu", "desktop", "amd64"},
	},
	{
		name:  "stopword dropped",
		input: "the quick brown fox",
		want:  []string{"quick", "brown", "fox"},
	},
	{
		// "iso" is in extensionTokens; "24"/"04" too short.
		name:  "extension dropped",
		input: "ubuntu-24.04.iso",
		want:  []string{"ubuntu"},
	},
	{
		// "a", "i", "ad" are under MinTokenBytes.
		name:  "short tokens dropped",
		input: "a i ad ben linux",
		want:  []string{"ben", "linux"},
	},
	{
		// German "die" is KEPT: the stopword list is English-only on
		// purpose.
		name:  "unicode letters, non-English stopword kept",
		input: "Über die Brücke",
		want:  []string{"über", "die", "brücke"},
	},
	{
		name:  "dedup after lowercase keeps first occurrence",
		input: "ubuntu ubuntu UBUNTU Ubuntu",
		want:  []string{"ubuntu"},
	},
	{
		name:  "mixed separators",
		input: "Linux_kernel-6.10.0[stable](amd64)",
		want:  []string{"linux", "kernel", "stable", "amd64"},
	},
	{
		// MinTokenBytes counts BYTES, not runes: "üü" is 2 runes but
		// 4 bytes and survives; "üb" is exactly 3 bytes and survives;
		// "ad" is 2 bytes and drops.
		name:  "bytes not runes",
		input: "üü üb ad",
		want:  []string{"üü", "üb"},
	},
	{
		// Digits pass through unchanged; length rule applies equally.
		name:  "digit tokens",
		input: "123 12 amd64",
		want:  []string{"123", "amd64"},
	},
	{
		// One input exercising every filter: short ("ad"), stopword
		// ("the"), extension ("mp4"), dedup ("linux" twice with case
		// variance).
		name:  "full filter chain interplay",
		input: "ad the mp4 Linux ad the mp4 LINUX",
		want:  []string{"linux"},
	},
	{
		name:  "lookup query tokens for MostDistinctive regression",
		input: "new ubuntu",
		want:  []string{"new", "ubuntu"},
	},
}

func TestTokenizeGolden(t *testing.T) {
	t.Parallel()
	for _, tc := range tokenizeGolden {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Tokenize(tc.input); !slices.Equal(got, tc.want) {
				t.Errorf("Tokenize(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestTokenizeNilSemantics(t *testing.T) {
	t.Parallel()
	if got := Tokenize(""); got != nil {
		t.Errorf("Tokenize(\"\") = %#v, want nil slice", got)
	}
	// Fully-filtered input is also nil, not an empty non-nil slice.
	if got := Tokenize("!!! a the mp4"); got != nil {
		t.Errorf("Tokenize(all-filtered) = %#v, want nil slice", got)
	}
	if got := TokenizeAll(""); got != nil {
		t.Errorf("TokenizeAll(\"\") = %#v, want nil slice", got)
	}
}

func TestTokenizeCap(t *testing.T) {
	t.Parallel()
	// Nine distinct surviving words: the cap keeps exactly the first eight
	// in appearance order.
	got := Tokenize("alpha beta gamma delta epsilon zeta eta theta iota")
	want := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta"}
	if !slices.Equal(got, want) {
		t.Errorf("Tokenize(9 words) = %v, want first 8 %v", got, want)
	}
	got = Tokenize("alpha beta gamma delta epsilon zeta eta theta iota kappa")
	if len(got) > MaxKeywordsPerTorrent {
		t.Errorf("len = %d, want <= %d", len(got), MaxKeywordsPerTorrent)
	}
}

func TestTokenizeAllUncapped(t *testing.T) {
	t.Parallel()
	input := "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda omega"
	want := []string{
		"alpha", "beta", "gamma", "delta", "epsilon", "zeta",
		"eta", "theta", "iota", "kappa", "lambda", "omega",
	}
	if got := TokenizeAll(input); !slices.Equal(got, want) {
		t.Errorf("TokenizeAll = %v, want all 12 %v", got, want)
	}
}

// mostDistinctiveGolden freezes byte-length ranking and the first-longest
// tie-break.
var mostDistinctiveGolden = []struct {
	name   string
	tokens []string
	want   string
}{
	{
		name:   "longest wins regardless of position",
		tokens: []string{"ubuntu", "desktop", "amd64"},
		want:   "desktop",
	},
	{
		name:   "tie-break keeps earliest",
		tokens: []string{"aaa", "bbb"},
		want:   "aaa",
	},
	{
		name:   "later longer token beats earlier shorter",
		tokens: []string{"bbb", "aaaa", "aaa"},
		want:   "aaaa",
	},
	{
		// BYTE length: "üü" is 4 bytes (2 runes) and beats 3-byte "abc".
		name:   "byte length not rune length",
		tokens: []string{"abc", "üü"},
		want:   "üü",
	},
	{
		name:   "single token",
		tokens: []string{"linux"},
		want:   "linux",
	},
	{
		name:   "nil input",
		tokens: nil,
		want:   "",
	},
	{
		name:   "empty input",
		tokens: []string{},
		want:   "",
	},
}

func TestMostDistinctiveGolden(t *testing.T) {
	t.Parallel()
	for _, tc := range mostDistinctiveGolden {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := MostDistinctive(tc.tokens); got != tc.want {
				t.Errorf("MostDistinctive(%v) = %q, want %q", tc.tokens, got, tc.want)
			}
		})
	}
}

// TestMostDistinctiveLookupRegression pins DECISIONS C16: the legacy DHT
// lookup took tokens[0] and searched "new ubuntu" by querying "new". The
// lookup-token chooser is MostDistinctive over Tokenize output, which must
// pick "ubuntu".
func TestMostDistinctiveLookupRegression(t *testing.T) {
	t.Parallel()
	if got := MostDistinctive(Tokenize("new ubuntu")); got != "ubuntu" {
		t.Errorf("MostDistinctive(Tokenize(%q)) = %q, want %q", "new ubuntu", got, "ubuntu")
	}
}
